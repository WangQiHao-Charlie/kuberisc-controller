/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	riscv1alpha1 "github.com/wangqihao-charlie/controller/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

// ActionRequestReconciler reconciles a ActionRequest object
type ActionRequestReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=risc.dev,resources=actionrequests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=risc.dev,resources=actionrequests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=risc.dev,resources=actionrequests/finalizers,verbs=update
// +kubebuilder:rbac:groups=risc.dev,resources=nodeactions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=risc.dev,resources=nodeactions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the ActionRequest object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.21.0/pkg/reconcile
func (r *ActionRequestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var ar riscv1alpha1.ActionRequest
	if err := r.Get(ctx, req.NamespacedName, &ar); err != nil {
		// Ignore not found to support deletes
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion with finalizer
	if ar.DeletionTimestamp != nil {
		if err := r.deleteOwnedNodeActions(ctx, &ar); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.removeFinalizer(ctx, &ar)
	}
	if err := r.ensureFinalizer(ctx, &ar); err != nil {
		return ctrl.Result{}, err
	}

	// Select target pods by simple matchLabels
	pods, err := r.listTargetPods(ctx, ar.Namespace, ar.Spec.Selector.PodSelector)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Build fanout targets
	targets := r.resolveTargets(pods)

	// List existing NodeActions owned by this AR
	ownedNAs, err := r.listOwnedNodeActions(ctx, &ar)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Plan creations for missing targets
	toCreate := r.planFanout(&ar, targets, ownedNAs)

	// Apply parallelism limit
	running := 0
	for i := range ownedNAs {
		if strings.EqualFold(ownedNAs[i].Status.Phase, "Running") {
			running++
		}
	}
	capacity := 1
	if ar.Spec.Strategy != nil && ar.Spec.Strategy.Parallelism > 0 {
		capacity = ar.Spec.Strategy.Parallelism
	}
	capacity -= running
	if capacity < 0 {
		capacity = 0
	}
	if len(toCreate) > capacity {
		toCreate = toCreate[:capacity]
	}

	// Create NodeActions
	for i := range toCreate {
		na := r.buildNodeAction(&ar, toCreate[i])
		if err := ctrl.SetControllerReference(&ar, na, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, na); client.IgnoreAlreadyExists(err) != nil {
			return ctrl.Result{}, err
		}
		log.V(1).Info("created NodeAction", "name", na.Name)
		if r.Recorder != nil {
			r.Recorder.Eventf(&ar, corev1.EventTypeNormal, "FanoutCreated", "Created NodeAction %s", na.Name)
		}
	}

	// Maybe retry failures (best-effort minimal impl)
	if ar.Spec.Strategy != nil && ar.Spec.Strategy.RetryLimit > 0 {
		if err := r.maybeRetryFailures(ctx, &ar); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Refresh owned list after creations
	ownedNAs, err = r.listOwnedNodeActions(ctx, &ar)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Aggregate status and patch
	counts := r.countPhases(ownedNAs)
	counts.Total = len(targets)
	phase, reason, done := r.evaluateCompletion(&ar, counts)
	if err := r.patchStatus(ctx, &ar, counts, phase, reason); err != nil {
		return ctrl.Result{}, err
	}

	if done {
		log.V(1).Info("ActionRequest completed", "reason", reason)
		return ctrl.Result{}, nil
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ActionRequestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// Set event recorder for ActionRequest events
	r.Recorder = mgr.GetEventRecorderFor("actionrequest")
	// Index NodeAction by owner (ActionRequest) UID to speed up lists
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &riscv1alpha1.NodeAction{}, "metadata.ownerReferences.uid",
		func(obj client.Object) []string {
			na := obj.(*riscv1alpha1.NodeAction)
			for _, o := range na.OwnerReferences {
				if o.Kind == "ActionRequest" {
					return []string{string(o.UID)}
				}
			}
			return nil
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&riscv1alpha1.ActionRequest{}).
		Owns(&riscv1alpha1.NodeAction{}).
		Named("actionrequest").
		Complete(r)
}

// ---- helpers ----

const (
	arFinalizer         = "risc.dev/finalizer"
	subjectIDAnnotation = "kuberisc.io/subject-id"
)

func (r *ActionRequestReconciler) ensureFinalizer(ctx context.Context, ar *riscv1alpha1.ActionRequest) error {
	for _, f := range ar.Finalizers {
		if f == arFinalizer {
			return nil
		}
	}
	base := ar.DeepCopy()
	ar.Finalizers = append(ar.Finalizers, arFinalizer)
	return r.Patch(ctx, ar, client.MergeFrom(base))
}

func (r *ActionRequestReconciler) removeFinalizer(ctx context.Context, ar *riscv1alpha1.ActionRequest) error {
	kept := make([]string, 0, len(ar.Finalizers))
	for _, f := range ar.Finalizers {
		if f != arFinalizer {
			kept = append(kept, f)
		}
	}
	if len(kept) == len(ar.Finalizers) {
		return nil
	}
	base := ar.DeepCopy()
	ar.Finalizers = kept
	return r.Patch(ctx, ar, client.MergeFrom(base))
}

func (r *ActionRequestReconciler) deleteOwnedNodeActions(ctx context.Context, ar *riscv1alpha1.ActionRequest) error {
	var list riscv1alpha1.NodeActionList
	if err := r.List(ctx, &list,
		client.InNamespace(ar.Namespace),
		client.MatchingFields{"metadata.ownerReferences.uid": string(ar.UID)},
	); err != nil {
		return err
	}
	for i := range list.Items {
		_ = r.Delete(ctx, &list.Items[i])
	}
	return nil
}

func (r *ActionRequestReconciler) listTargetPods(ctx context.Context, namespace string, matchLabels map[string]string) ([]corev1.Pod, error) {
	var pods corev1.PodList
	opts := []client.ListOption{client.InNamespace(namespace)}
	if len(matchLabels) > 0 {
		opts = append(opts, client.MatchingLabels(matchLabels))
	}
	if err := r.List(ctx, &pods, opts...); err != nil {
		return nil, err
	}
	return pods.Items, nil
}

type target struct {
	NodeName  string
	SubjectID string
	PodName   string
}

func (r *ActionRequestReconciler) resolveTargets(pods []corev1.Pod) []target {
	res := make([]target, 0, len(pods))
	for i := range pods {
		t := target{
			NodeName:  pods[i].Spec.NodeName,
			SubjectID: pods[i].Annotations[subjectIDAnnotation],
			PodName:   pods[i].Name,
		}
		// If pod not scheduled or missing annotation, still include with best effort
		res = append(res, t)
	}
	return res
}

func (r *ActionRequestReconciler) listOwnedNodeActions(ctx context.Context, ar *riscv1alpha1.ActionRequest) ([]riscv1alpha1.NodeAction, error) {
	var list riscv1alpha1.NodeActionList
	if err := r.List(ctx, &list,
		client.InNamespace(ar.Namespace),
		client.MatchingFields{"metadata.ownerReferences.uid": string(ar.UID)},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

func keyForTarget(t target) string {
	return fmt.Sprintf("%s|%s", t.NodeName, t.SubjectID)
}

func (r *ActionRequestReconciler) planFanout(ar *riscv1alpha1.ActionRequest, targets []target, existing []riscv1alpha1.NodeAction) []target {
	existingKeys := map[string]struct{}{}
	for i := range existing {
		k := fmt.Sprintf("%s|%s", existing[i].Spec.NodeName, existing[i].Spec.ResolvedSubjectID)
		existingKeys[k] = struct{}{}
	}
	out := make([]target, 0)
	for i := range targets {
		k := keyForTarget(targets[i])
		if _, ok := existingKeys[k]; !ok {
			out = append(out, targets[i])
		}
	}
	return out
}

func sanitizeName(s string) string {
	if s == "" {
		return "unknown"
	}
	// very simple sanitization for k8s names
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "/", "-")
	s = strings.ReplaceAll(s, ":", "-")
	s = strings.ReplaceAll(s, " ", "-")
	return s
}

func (r *ActionRequestReconciler) buildNodeAction(ar *riscv1alpha1.ActionRequest, t target) *riscv1alpha1.NodeAction {
	name := fmt.Sprintf("%s-%s-%s-", ar.Name, sanitizeName(t.NodeName), sanitizeName(t.SubjectID))
	na := &riscv1alpha1.NodeAction{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    ar.Namespace,
			GenerateName: name,
			Labels: map[string]string{
				"risc.dev/ar": ar.Name,
				// Add node label so agents using labelSelector (NA_NODE_LABEL)
				// can efficiently discover NodeActions for their node.
				"risc.dev/node": t.NodeName,
			},
		},
		Spec: riscv1alpha1.NodeActionSpec{
			NodeName:          t.NodeName,
			InstructionRef:    ar.Spec.InstructionRef,
			ResolvedSubjectID: t.SubjectID,
			Params:            ar.Spec.Params,
			TimeoutSeconds:    ar.Spec.TimeoutSeconds,
		},
	}
	return na
}

func (r *ActionRequestReconciler) maybeRetryFailures(ctx context.Context, ar *riscv1alpha1.ActionRequest) error {
	var list riscv1alpha1.NodeActionList
	if err := r.List(ctx, &list,
		client.InNamespace(ar.Namespace),
		client.MatchingFields{"metadata.ownerReferences.uid": string(ar.UID)},
	); err != nil {
		return err
	}
	retryLimit := ar.Spec.Strategy.RetryLimit
	for i := range list.Items {
		na := &list.Items[i]
		if !strings.EqualFold(na.Status.Phase, "Failed") {
			continue
		}
		tries := 0
		if v := na.Annotations["risc.dev/try"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				tries = n
			}
		}
		if tries >= retryLimit {
			continue
		}
		base := na.DeepCopy()
		if na.Annotations == nil {
			na.Annotations = map[string]string{}
		}
		na.Annotations["risc.dev/try"] = strconv.Itoa(tries + 1)
		if err := r.Patch(ctx, na, client.MergeFrom(base)); err != nil {
			return err
		}
		// naive approach: delete to allow re-create on next loop, or an external reconciler will restart it
	}
	return nil
}

func (r *ActionRequestReconciler) countPhases(nas []riscv1alpha1.NodeAction) *riscv1alpha1.Counts {
	c := &riscv1alpha1.Counts{}
	for i := range nas {
		switch strings.ToLower(nas[i].Status.Phase) {
		case "running":
			c.Running++
		case "succeeded":
			c.Succeeded++
		case "failed":
			c.Failed++
		default:
			// treat others as pending
		}
	}
	c.Total = c.Running + c.Succeeded + c.Failed // approximate; true total comes from targets
	return c
}

func (r *ActionRequestReconciler) evaluateCompletion(ar *riscv1alpha1.ActionRequest, c *riscv1alpha1.Counts) (phase, reason string, done bool) {
	expected := c.Total
	doneCount := c.Succeeded + c.Failed
	switch {
	case expected == 0:
		return "Pending", "NoTargets", false
	case doneCount < expected && c.Running > 0:
		return "Running", "InProgress", false
	case doneCount < expected:
		return "Aggregating", "Waiting", false
	default:
		// completion policy
		if ar.Spec.Strategy != nil && ar.Spec.Strategy.CompletionPolicy != nil {
			pol := ar.Spec.Strategy.CompletionPolicy
			switch strings.ToLower(pol.Type) {
			case "all":
				if c.Succeeded == expected {
					return "Completed", "Succeeded", true
				}
				return "Completed", "Partial", true
			case "atleastn":
				if c.Succeeded >= pol.AtLeastN {
					return "Completed", "Partial", true
				}
				return "Completed", "Failed", true
			}
		}
		if c.Failed == 0 {
			return "Completed", "Succeeded", true
		}
		return "Completed", "Partial", true
	}
}

func (r *ActionRequestReconciler) patchStatus(ctx context.Context, ar *riscv1alpha1.ActionRequest, counts *riscv1alpha1.Counts, phase, reason string) error {
	base := ar.DeepCopy()
	ar.Status.Counts = counts
	ar.Status.Phase = phase
	ar.Status.Reason = reason
	changed := base.Status.Phase != phase || base.Status.Reason != reason
	if err := r.Status().Patch(ctx, ar, client.MergeFrom(base)); err != nil {
		return err
	}
	if changed && r.Recorder != nil {
		// Emit an event when phase/reason changes
		r.Recorder.Eventf(ar, corev1.EventTypeNormal, "Aggregated", "phase=%s reason=%s counts: total=%d running=%d succeeded=%d failed=%d", phase, reason, counts.Total, counts.Running, counts.Succeeded, counts.Failed)
	}
	return nil
}
