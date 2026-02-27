package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
)

const (
	// maxBodySize limits the request body to 1MB.
	maxBodySize = 1 << 20

	// labelPrefix is the nexa.io/ label namespace we validate.
	labelPrefix = "nexa.io/"

	// labelOrg is the pod label for organizational identity.
	labelOrg = "nexa.io/org"

	// labelPrivacy is the pod label for privacy level.
	labelPrivacy = "nexa.io/privacy"
)

// Handler processes admission review requests for nexa.io/* label validation.
type Handler struct {
	config *Config
}

// NewHandler creates an admission handler with the given config.
func NewHandler(cfg *Config) *Handler {
	return &Handler{config: cfg}
}

// ServeHTTP routes requests to the appropriate handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/validate":
		h.handleValidate(w, r)
	case "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	default:
		http.NotFound(w, r)
	}
}

func (h *Handler) handleValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize))
	if err != nil || len(body) == 0 {
		writeError(w, "failed to read request body")
		return
	}

	var review admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &review); err != nil {
		writeError(w, fmt.Sprintf("failed to decode AdmissionReview: %v", err))
		return
	}

	if review.Request == nil {
		writeError(w, "AdmissionReview has no request")
		return
	}

	response := h.validate(review.Request)
	response.UID = review.Request.UID

	review.Response = response
	review.Request = nil

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(review); err != nil {
		klog.ErrorS(err, "failed to encode admission response")
	}
}

func (h *Handler) validate(req *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	// Skip sub-resources (e.g., pods/status).
	if req.SubResource != "" {
		return allowed("sub-resource requests are not validated")
	}

	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		return denied(fmt.Sprintf("failed to decode pod: %v", err))
	}

	// On UPDATE, only validate if nexa.io/* labels changed.
	if req.Operation == admissionv1.Update && req.OldObject.Raw != nil {
		var oldPod corev1.Pod
		if err := json.Unmarshal(req.OldObject.Raw, &oldPod); err == nil {
			if !nexaLabelsChanged(oldPod.Labels, pod.Labels) {
				return allowed("nexa.io labels unchanged")
			}
		}
	}

	// Extract nexa.io/* labels from the pod.
	orgValue, hasOrg := pod.Labels[labelOrg]
	privacyValue, hasPrivacy := pod.Labels[labelPrivacy]

	// If no nexa.io labels are set, allow unconditionally.
	if !hasOrg && !hasPrivacy {
		return allowed("no nexa.io labels present")
	}

	// Validate privacy value regardless of namespace rules.
	if hasPrivacy && !validPrivacyLevels[privacyValue] {
		return denied(fmt.Sprintf(
			"invalid privacy level %q on nexa.io/privacy; allowed values: \"standard\", \"high\"",
			privacyValue,
		))
	}

	// Look up namespace rules.
	ns := req.Namespace
	rule := h.config.RuleForNamespace(ns)
	if rule == nil {
		return denied(fmt.Sprintf(
			"no admission rules configured for namespace %q; cannot validate nexa.io labels",
			ns,
		))
	}

	// Validate org label.
	if hasOrg {
		if !stringInSlice(orgValue, rule.AllowedOrgs) {
			return denied(fmt.Sprintf(
				"namespace %q is not authorized for org %q; allowed orgs: %v",
				ns, orgValue, rule.AllowedOrgs,
			))
		}
	}

	// Validate privacy label against namespace-specific allowed list.
	if hasPrivacy {
		if !stringInSlice(privacyValue, rule.AllowedPrivacy) {
			return denied(fmt.Sprintf(
				"namespace %q is not authorized for privacy level %q; allowed levels: %v",
				ns, privacyValue, rule.AllowedPrivacy,
			))
		}
	}

	return allowed("labels validated")
}

// nexaLabelsChanged returns true if any nexa.io/* label differs between old and new.
func nexaLabelsChanged(old, new map[string]string) bool {
	for k, v := range new {
		if strings.HasPrefix(k, labelPrefix) {
			if old[k] != v {
				return true
			}
		}
	}
	for k := range old {
		if strings.HasPrefix(k, labelPrefix) {
			if _, ok := new[k]; !ok {
				return true
			}
		}
	}
	return false
}

func allowed(msg string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: true,
		Result:  &metav1.Status{Message: msg},
	}
}

func denied(msg string) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		Allowed: false,
		Result: &metav1.Status{
			Message: msg,
			Code:    http.StatusForbidden,
		},
	}
}

func writeError(w http.ResponseWriter, msg string) {
	klog.ErrorS(nil, msg)
	http.Error(w, msg, http.StatusBadRequest)
}

func stringInSlice(s string, slice []string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
