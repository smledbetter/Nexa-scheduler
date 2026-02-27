package webhook

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func testConfig() *Config {
	return &Config{
		Rules: []NamespaceRule{
			{Namespace: "alpha-workloads", AllowedOrgs: []string{"alpha"}, AllowedPrivacy: []string{"standard", "high"}},
			{Namespace: "beta-workloads", AllowedOrgs: []string{"beta"}, AllowedPrivacy: []string{"standard"}},
			{Namespace: "*", AllowedOrgs: []string{"default-org"}, AllowedPrivacy: []string{"standard", "high"}},
		},
	}
}

func makeReview(pod *corev1.Pod, ns string, op admissionv1.Operation, oldPod *corev1.Pod) *admissionv1.AdmissionReview {
	podRaw, _ := json.Marshal(pod)
	req := &admissionv1.AdmissionRequest{
		UID:       "test-uid",
		Operation: op,
		Namespace: ns,
		Object:    runtime.RawExtension{Raw: podRaw},
		Resource:  metav1.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"},
	}
	if oldPod != nil {
		oldRaw, _ := json.Marshal(oldPod)
		req.OldObject = runtime.RawExtension{Raw: oldRaw}
	}
	return &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{APIVersion: "admission.k8s.io/v1", Kind: "AdmissionReview"},
		Request:  req,
	}
}

func makePod(labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "test-pod",
			Labels: labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "c", Image: "busybox"}},
		},
	}
}

func postReview(t *testing.T, handler http.Handler, review *admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	t.Helper()
	body, err := json.Marshal(review)
	if err != nil {
		t.Fatalf("marshal review: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp admissionv1.AdmissionReview
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Response == nil {
		t.Fatal("response is nil")
	}
	return resp.Response
}

func TestHandlerValidate(t *testing.T) {
	h := NewHandler(testConfig())

	tests := []struct {
		name    string
		labels  map[string]string
		ns      string
		op      admissionv1.Operation
		oldPod  *corev1.Pod
		allowed bool
		msgHas  string
	}{
		{
			name:    "no nexa labels allowed",
			labels:  map[string]string{"app": "myapp"},
			ns:      "alpha-workloads",
			op:      admissionv1.Create,
			allowed: true,
			msgHas:  "no nexa.io labels",
		},
		{
			name:    "valid org admitted",
			labels:  map[string]string{"nexa.io/org": "alpha"},
			ns:      "alpha-workloads",
			op:      admissionv1.Create,
			allowed: true,
			msgHas:  "validated",
		},
		{
			name:    "spoofed org rejected",
			labels:  map[string]string{"nexa.io/org": "alpha"},
			ns:      "beta-workloads",
			op:      admissionv1.Create,
			allowed: false,
			msgHas:  "not authorized for org",
		},
		{
			name:    "no rule for namespace falls to wildcard",
			labels:  map[string]string{"nexa.io/org": "default-org"},
			ns:      "unknown",
			op:      admissionv1.Create,
			allowed: true,
			msgHas:  "validated",
		},
		{
			name:    "no rule and no wildcard rejects",
			labels:  map[string]string{"nexa.io/org": "alpha"},
			ns:      "unknown",
			op:      admissionv1.Create,
			allowed: false,
			msgHas:  "not authorized for org",
		},
		{
			name:    "valid privacy admitted",
			labels:  map[string]string{"nexa.io/privacy": "high"},
			ns:      "alpha-workloads",
			op:      admissionv1.Create,
			allowed: true,
			msgHas:  "validated",
		},
		{
			name:    "invalid privacy value rejected",
			labels:  map[string]string{"nexa.io/privacy": "extreme"},
			ns:      "alpha-workloads",
			op:      admissionv1.Create,
			allowed: false,
			msgHas:  "invalid privacy level",
		},
		{
			name:    "both labels valid",
			labels:  map[string]string{"nexa.io/org": "alpha", "nexa.io/privacy": "high"},
			ns:      "alpha-workloads",
			op:      admissionv1.Create,
			allowed: true,
			msgHas:  "validated",
		},
		{
			name:    "org valid but privacy restricted for namespace",
			labels:  map[string]string{"nexa.io/org": "beta", "nexa.io/privacy": "high"},
			ns:      "beta-workloads",
			op:      admissionv1.Create,
			allowed: false,
			msgHas:  "not authorized for privacy level",
		},
		{
			name:    "wildcard rule applies for unmatched namespace",
			labels:  map[string]string{"nexa.io/org": "default-org"},
			ns:      "some-ns",
			op:      admissionv1.Create,
			allowed: true,
			msgHas:  "validated",
		},
		{
			name:    "update with no label change passes",
			labels:  map[string]string{"nexa.io/org": "alpha"},
			ns:      "alpha-workloads",
			op:      admissionv1.Update,
			oldPod:  makePod(map[string]string{"nexa.io/org": "alpha"}),
			allowed: true,
			msgHas:  "unchanged",
		},
		{
			name:    "update adding org label validated",
			labels:  map[string]string{"nexa.io/org": "alpha"},
			ns:      "alpha-workloads",
			op:      admissionv1.Update,
			oldPod:  makePod(map[string]string{}),
			allowed: true,
			msgHas:  "validated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := makePod(tt.labels)
			review := makeReview(pod, tt.ns, tt.op, tt.oldPod)
			resp := postReview(t, h, review)

			if resp.Allowed != tt.allowed {
				t.Errorf("allowed = %v, want %v; message: %s", resp.Allowed, tt.allowed, resp.Result.Message)
			}
			if tt.msgHas != "" && !contains(resp.Result.Message, tt.msgHas) {
				t.Errorf("message %q does not contain %q", resp.Result.Message, tt.msgHas)
			}
		})
	}
}

func TestHandlerSubresource(t *testing.T) {
	h := NewHandler(testConfig())

	pod := makePod(map[string]string{"nexa.io/org": "spoofed"})
	review := makeReview(pod, "beta-workloads", admissionv1.Update, nil)
	review.Request.SubResource = "status"

	resp := postReview(t, h, review)
	if !resp.Allowed {
		t.Errorf("sub-resource request should be allowed, got denied: %s", resp.Result.Message)
	}
}

func TestHandlerMalformedBody(t *testing.T) {
	h := NewHandler(testConfig())

	req := httptest.NewRequest(http.MethodPost, "/validate", bytes.NewReader([]byte(`{broken`)))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want status 400, got %d", rec.Code)
	}
}

func TestHandlerEmptyBody(t *testing.T) {
	h := NewHandler(testConfig())

	req := httptest.NewRequest(http.MethodPost, "/validate", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("want status 400, got %d", rec.Code)
	}
}

func TestHandlerHealthz(t *testing.T) {
	h := NewHandler(testConfig())

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("want status 200, got %d", rec.Code)
	}
}

func TestHandlerNotFound(t *testing.T) {
	h := NewHandler(testConfig())

	req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("want status 404, got %d", rec.Code)
	}
}

func TestNexaLabelsChanged(t *testing.T) {
	tests := []struct {
		name    string
		old     map[string]string
		new     map[string]string
		changed bool
	}{
		{"no nexa labels", map[string]string{"a": "b"}, map[string]string{"a": "c"}, false},
		{"same nexa labels", map[string]string{"nexa.io/org": "a"}, map[string]string{"nexa.io/org": "a"}, false},
		{"nexa label changed", map[string]string{"nexa.io/org": "a"}, map[string]string{"nexa.io/org": "b"}, true},
		{"nexa label added", map[string]string{}, map[string]string{"nexa.io/org": "a"}, true},
		{"nexa label removed", map[string]string{"nexa.io/org": "a"}, map[string]string{}, true},
		{"nil old", nil, map[string]string{"nexa.io/org": "a"}, true},
		{"nil both", nil, nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nexaLabelsChanged(tt.old, tt.new); got != tt.changed {
				t.Errorf("nexaLabelsChanged = %v, want %v", got, tt.changed)
			}
		})
	}
}
