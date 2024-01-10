package admission

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"

	"sigs.k8s.io/controller-runtime/pkg/client"

	policiesv1 "github.com/kubewarden/kubewarden-controller/pkg/apis/policies/v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubewarden/kubewarden-controller/internal/pkg/constants"
)

//+kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=mutatingwebhookconfigurations,verbs=get;list;watch;create;update;patch

func (r *Reconciler) ReconcileMutatingWebhookConfiguration(
	ctx context.Context,
	policy policiesv1.Policy,
	admissionSecret *corev1.Secret,
	policyServerNameWithPrefix string,
) error {
	webhook := r.mutatingWebhookConfiguration(policy, admissionSecret, policyServerNameWithPrefix)
	err := r.Client.Create(ctx, webhook)
	if err == nil {
		return nil
	}
	if apierrors.IsAlreadyExists(err) {
		return r.updateMutatingWebhook(ctx, policy, webhook)
	}
	return fmt.Errorf("cannot reconcile mutating webhook: %w", err)
}

func (r *Reconciler) updateMutatingWebhook(ctx context.Context,
	policy policiesv1.Policy,
	newWebhook *admissionregistrationv1.MutatingWebhookConfiguration,
) error {
	var originalWebhook admissionregistrationv1.MutatingWebhookConfiguration

	err := r.Client.Get(ctx, client.ObjectKey{
		Name: policy.GetUniqueName(),
	}, &originalWebhook)
	if err != nil && apierrors.IsNotFound(err) {
		return fmt.Errorf("cannot retrieve mutating webhook: %w", err)
	}

	patch := originalWebhook.DeepCopy()

	if patch.ObjectMeta.Labels == nil {
		patch.ObjectMeta.Labels = make(map[string]string)
	}
	for key, value := range newWebhook.ObjectMeta.Labels {
		patch.ObjectMeta.Labels[key] = value
	}

	if patch.ObjectMeta.Annotations == nil {
		patch.ObjectMeta.Annotations = make(map[string]string)
	}
	for key, value := range newWebhook.ObjectMeta.Annotations {
		patch.ObjectMeta.Annotations[key] = value
	}

	if !reflect.DeepEqual(originalWebhook.Webhooks, newWebhook.Webhooks) {
		patch.Webhooks = newWebhook.Webhooks
	}

	err = r.Client.Patch(ctx, patch, client.MergeFrom(&originalWebhook))
	if err != nil {
		return fmt.Errorf("cannot patch mutating webhook: %w", err)
	}

	return nil
}

func (r *Reconciler) mutatingWebhookConfiguration(
	policy policiesv1.Policy,
	admissionSecret *corev1.Secret,
	policyServerName string,
) *admissionregistrationv1.MutatingWebhookConfiguration {
	admissionPath := filepath.Join("/validate", policy.GetUniqueName())
	admissionPort := int32(constants.PolicyServerPort)

	service := admissionregistrationv1.ServiceReference{
		Namespace: r.DeploymentsNamespace,
		Name:      policyServerName,
		Path:      &admissionPath,
		Port:      &admissionPort,
	}

	sideEffects := policy.GetSideEffects()
	if sideEffects == nil {
		noneSideEffects := admissionregistrationv1.SideEffectClassNone
		sideEffects = &noneSideEffects
	}

	policyScope := "namespace"
	if policy.GetNamespace() == "" {
		policyScope = "cluster"
	}

	return &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: metav1.ObjectMeta{
			Name: policy.GetUniqueName(),
			Labels: map[string]string{
				"kubewarden": "true",
				constants.WebhookConfigurationPolicyScopeLabelKey: policyScope,
			},
			Annotations: map[string]string{
				constants.WebhookConfigurationPolicyNameAnnotationKey:      policy.GetName(),
				constants.WebhookConfigurationPolicyNamespaceAnnotationKey: policy.GetNamespace(),
			},
		},
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{
				Name: fmt.Sprintf("%s.kubewarden.admission", policy.GetUniqueName()),
				ClientConfig: admissionregistrationv1.WebhookClientConfig{
					Service:  &service,
					CABundle: admissionSecret.Data[constants.PolicyServerCARootPemName],
				},
				Rules:                   policy.GetRules(),
				FailurePolicy:           policy.GetFailurePolicy(),
				MatchPolicy:             policy.GetMatchPolicy(),
				NamespaceSelector:       policy.GetUpdatedNamespaceSelector(r.DeploymentsNamespace),
				ObjectSelector:          policy.GetObjectSelector(),
				SideEffects:             sideEffects,
				TimeoutSeconds:          policy.GetTimeoutSeconds(),
				AdmissionReviewVersions: []string{"v1"},
			},
		},
	}
}
