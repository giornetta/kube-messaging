package controller

import (
	"context"
	"encoding/json"
	"fmt"

	appsv1alpha1 "github.com/giornetta/kube-messaging/api/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// Update the sendWebhookNotification method:
func (r *NotificationReconciler) sendWebhookNotification(ctx context.Context, notification *appsv1alpha1.Notification) error {
	logger := log.FromContext(ctx)

	// Create a unique job name
	jobName := fmt.Sprintf("webhook-%s-%s-%d",
		notification.Namespace,
		notification.Name,
		notification.Status.SentCount+1)

	// Ensure the name isn't too long for Kubernetes (63 chars max)
	if len(jobName) > 63 {
		jobName = jobName[:63]
	}

	// Prepare webhook data as JSON
	webhookData := map[string]interface{}{
		"url":     notification.Spec.Destination.Webhook.URL,
		"headers": notification.Spec.Destination.Webhook.Headers,
		"body":    notification.Spec.Body,
		"metadata": map[string]string{
			"notificationName":      notification.Name,
			"notificationNamespace": notification.Namespace,
			"sentCount":             fmt.Sprintf("%d", notification.Status.SentCount+1),
		},
	}

	webhookDataJSON, err := json.Marshal(webhookData)
	if err != nil {
		return fmt.Errorf("failed to marshal webhook data: %w", err)
	}

	// Check if job already exists (idempotency)
	var existingJob batchv1.Job
	err = r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: notification.Namespace}, &existingJob)
	if err == nil {
		// Job already exists, we can consider this a success
		logger.Info("Webhook job already exists", "job", jobName)
		return nil
	}

	// Create the job
	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: notification.Namespace,
			Labels: map[string]string{
				"app":               "notification-operator",
				"notification":      notification.Name,
				"notification-type": "webhook",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: notification.APIVersion,
					Kind:       notification.Kind,
					Name:       notification.Name,
					UID:        notification.UID,
					Controller: ptr.To(true),
				},
			},
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: ptr.To(int32(3)), // Retry up to 3 times
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyNever,
					Containers: []corev1.Container{
						{
							Name:  "webhook-sender",
							Image: r.WebhookImage,
							Env: []corev1.EnvVar{
								{
									Name:  "WEBHOOK_DATA",
									Value: string(webhookDataJSON),
								},
							},
						},
					},
				},
			},
		},
	}

	// Create the job
	err = r.Create(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to create webhook job: %w", err)
	}

	logger.Info("Created webhook notification job", "job", jobName)

	return nil
}
