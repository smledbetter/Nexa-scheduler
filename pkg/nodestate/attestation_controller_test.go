package nodestate

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/nexascheduler/nexa/pkg/attestation"
)

// fakeAttester implements attestation.Attester for testing.
type fakeAttester struct {
	results map[string]*attestation.Result
	err     error
	called  []string
}

func (f *fakeAttester) Verify(_ context.Context, nodeID string) (*attestation.Result, error) {
	f.called = append(f.called, nodeID)
	if f.err != nil {
		return nil, f.err
	}
	if r, ok := f.results[nodeID]; ok {
		return r, nil
	}
	return &attestation.Result{Attested: false}, nil
}

func TestAttestationController_VerifyNode_Success(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	node := makeTestNode("tee-node-1", map[string]string{"nexa.io/tee": "tdx"})
	client := fake.NewClientset(node)

	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tee-node-1": {
				Attested:    true,
				TrustAnchor: "intel-ta",
				Timestamp:   now,
			},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)

	err := ac.verifyNode(ctx, "tee-node-1")
	if err != nil {
		t.Fatalf("verifyNode failed: %v", err)
	}

	// Verify the patch was applied.
	patchedNode, err := client.CoreV1().Nodes().Get(ctx, "tee-node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}

	if patchedNode.Labels[LabelTEEAttested] != "true" {
		t.Errorf("tee-attested = %q, want true", patchedNode.Labels[LabelTEEAttested])
	}
	if patchedNode.Labels[LabelTEETrustAnchor] != "intel-ta" {
		t.Errorf("tee-trust-anchor = %q, want intel-ta", patchedNode.Labels[LabelTEETrustAnchor])
	}
	tsStr := patchedNode.Labels[LabelTEEAttestationTime]
	if tsStr == "" {
		t.Fatal("tee-attestation-time label missing")
	}
	ts, err := time.Parse(time.RFC3339, tsStr)
	if err != nil {
		t.Fatalf("malformed attestation timestamp: %v", err)
	}
	if !ts.Equal(now) {
		t.Errorf("attestation-time = %v, want %v", ts, now)
	}
}

func TestAttestationController_VerifyNode_FailClosed(t *testing.T) {
	node := makeTestNode("tee-node-2", map[string]string{"nexa.io/tee": "sev-snp"})
	client := fake.NewClientset(node)

	attester := &fakeAttester{err: errors.New("connection refused")}

	factory := informers.NewSharedInformerFactory(client, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)

	err := ac.verifyNode(ctx, "tee-node-2")
	if err != nil {
		t.Fatalf("verifyNode failed: %v", err)
	}

	patchedNode, err := client.CoreV1().Nodes().Get(ctx, "tee-node-2", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}

	if patchedNode.Labels[LabelTEEAttested] != "false" {
		t.Errorf("tee-attested = %q, want false (fail-closed)", patchedNode.Labels[LabelTEEAttested])
	}
}

func TestAttestationController_VerifyNode_NotAttested(t *testing.T) {
	node := makeTestNode("tee-node-3", map[string]string{"nexa.io/tee": "tdx"})
	client := fake.NewClientset(node)

	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tee-node-3": {Attested: false},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)

	err := ac.verifyNode(ctx, "tee-node-3")
	if err != nil {
		t.Fatalf("verifyNode failed: %v", err)
	}

	patchedNode, err := client.CoreV1().Nodes().Get(ctx, "tee-node-3", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get node: %v", err)
	}

	if patchedNode.Labels[LabelTEEAttested] != "false" {
		t.Errorf("tee-attested = %q, want false", patchedNode.Labels[LabelTEEAttested])
	}
}

func TestAttestationController_VerifyAllNodes_SkipsNonTEE(t *testing.T) {
	teeNode := makeTestNode("tee-node", map[string]string{"nexa.io/tee": "tdx"})
	plainNode := makeTestNode("plain-node", map[string]string{})
	noneNode := makeTestNode("tee-none-node", map[string]string{"nexa.io/tee": "none"})
	client := fake.NewClientset(teeNode, plainNode, noneNode)

	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tee-node": {Attested: true, Timestamp: time.Now()},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	// Access the node informer before starting so it registers.
	nodeInformer := factory.Core().V1().Nodes().Informer()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), nodeInformer.HasSynced) {
		t.Fatal("informer cache did not sync")
	}

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)
	ac.verifyAllNodes(ctx)

	if len(attester.called) != 1 {
		t.Errorf("expected 1 verification call (TEE node only), got %d: %v", len(attester.called), attester.called)
	}
	if len(attester.called) > 0 && attester.called[0] != "tee-node" {
		t.Errorf("verified %q, want tee-node", attester.called[0])
	}
}

func TestAttestationController_VerifyAllNodes_MultipleNodes(t *testing.T) {
	tdxNode := makeTestNode("tdx-node", map[string]string{"nexa.io/tee": "tdx"})
	sevNode := makeTestNode("sev-node", map[string]string{"nexa.io/tee": "sev-snp"})
	client := fake.NewClientset(tdxNode, sevNode)

	now := time.Now()
	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tdx-node": {Attested: true, TrustAnchor: "intel-ta", Timestamp: now},
			"sev-node": {Attested: true, TrustAnchor: "azure-maa", Timestamp: now},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	nodeInformer := factory.Core().V1().Nodes().Informer()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), nodeInformer.HasSynced) {
		t.Fatal("informer cache did not sync")
	}

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)
	ac.verifyAllNodes(ctx)

	if len(attester.called) != 2 {
		t.Errorf("expected 2 verification calls, got %d", len(attester.called))
	}
}

func TestAttestationController_PatchError(t *testing.T) {
	node := makeTestNode("tee-node", map[string]string{"nexa.io/tee": "tdx"})
	client := fake.NewClientset(node)

	// Inject a patch error.
	client.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("API server unavailable")
	})

	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tee-node": {Attested: true, Timestamp: time.Now()},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)

	err := ac.verifyNode(ctx, "tee-node")
	if err == nil {
		t.Fatal("expected error from patch failure, got nil")
	}
	if !strings.Contains(err.Error(), "API server unavailable") {
		t.Errorf("error = %q, want to contain 'API server unavailable'", err.Error())
	}
}

func TestAttestationController_Run_ContextCancellation(t *testing.T) {
	node := makeTestNode("tee-node", map[string]string{"nexa.io/tee": "tdx"})
	client := fake.NewClientset(node)

	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tee-node": {Attested: true, Timestamp: time.Now()},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	nodeInformer := factory.Core().V1().Nodes().Informer()

	ctx, cancel := context.WithCancel(context.Background())
	factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), nodeInformer.HasSynced) {
		t.Fatal("informer cache did not sync")
	}

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, 100*time.Millisecond)

	done := make(chan error, 1)
	go func() {
		done <- ac.Run(ctx)
	}()

	// Let it run one cycle, then cancel.
	time.Sleep(150 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancellation")
	}

	// Should have been called at least once (initial verification on start).
	if len(attester.called) < 1 {
		t.Error("expected at least 1 verification call")
	}
}

func TestAttestationController_VerifyNode_PatchFormat(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	node := makeTestNode("tee-node", map[string]string{"nexa.io/tee": "tdx"})
	client := fake.NewClientset(node)

	var capturedPatch []byte
	client.PrependReactor("patch", "nodes", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patchAction := action.(k8stesting.PatchAction)
		capturedPatch = patchAction.GetPatch()
		return false, nil, nil // let it proceed
	})

	attester := &fakeAttester{
		results: map[string]*attestation.Result{
			"tee-node": {Attested: true, TrustAnchor: "intel-ta", Timestamp: now},
		},
	}

	factory := informers.NewSharedInformerFactory(client, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory.Start(ctx.Done())
	factory.WaitForCacheSync(ctx.Done())

	ac := NewAttestationController(client, factory.Core().V1().Nodes().Lister(), attester, time.Minute)
	_ = ac.verifyNode(ctx, "tee-node")

	// Verify patch structure.
	var patch map[string]interface{}
	if err := json.Unmarshal(capturedPatch, &patch); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}

	metadata := patch["metadata"].(map[string]interface{})
	lbls := metadata["labels"].(map[string]interface{})

	if lbls[LabelTEEAttested] != "true" {
		t.Errorf("patch label %s = %v, want true", LabelTEEAttested, lbls[LabelTEEAttested])
	}
	if lbls[LabelTEETrustAnchor] != "intel-ta" {
		t.Errorf("patch label %s = %v, want intel-ta", LabelTEETrustAnchor, lbls[LabelTEETrustAnchor])
	}
	if lbls[LabelTEEAttestationTime] != now.Format(time.RFC3339) {
		t.Errorf("patch label %s = %v, want %s", LabelTEEAttestationTime, lbls[LabelTEEAttestationTime], now.Format(time.RFC3339))
	}
}
