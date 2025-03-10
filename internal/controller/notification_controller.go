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
	"time"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	appsv1alpha1 "github.com/giornetta/kube-messaging/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NotificationReconciler reconciles a Notification object
type NotificationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Docker image for webhook sender.
	WebhookImage string
}

// +kubebuilder:rbac:groups=app.kube-messaging.michelegiornetta.com,resources=notifications,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=app.kube-messaging.michelegiornetta.com,resources=notifications/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=app.kube-messaging.michelegiornetta.com,resources=notifications/finalizers,verbs=update
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs/status,verbs=get
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *NotificationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("Reconciling Notification", "name", req.Name, "namespace", req.Namespace)

	// Fetch the Notification instance
	var notification appsv1alpha1.Notification
	if err := r.Get(ctx, req.NamespacedName, &notification); err != nil {
		// We are returning the error if it's different than "NotFound", otherwise we're ignoring it and quitting early.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Skip reconciliation if notification is already completed
	if notification.Status.CurrentState == appsv1alpha1.NotificationCompleted || notification.Status.CurrentState == appsv1alpha1.NotificationFailed {
		logger.Info("Notification in terminal state, skipping reconciliation",
			"state", notification.Status.CurrentState,
			"name", notification.Name,
			"namespace", notification.Namespace)
		return ctrl.Result{}, nil
	}

	// Initialize status if empty
	statusInitialized, err := r.initializeStatusIfNeeded(ctx, &notification)
	if err != nil {
		return ctrl.Result{}, err
	}
	if statusInitialized {
		return ctrl.Result{Requeue: true}, nil
	}

	if notification.Spec.Destination.Email != nil || notification.Spec.Destination.Slack != nil {
		err := fmt.Errorf("destination not implemented")
		logger.Error(err, "Stopping reconciliation of notification")

		r.updateStatusWithError(ctx, &notification, "DestinationUnimplemented", err.Error())
		return ctrl.Result{}, nil
	}

	// Check job status for webhook notifications.
	if notification.Spec.Destination.Webhook != nil {
		result, err := r.checkJobStatus(ctx, &notification)
		if err != nil || result.Requeue || result.RequeueAfter > 0 {
			return result, err
		}
	}

	// Proceed only if notification has not been completed/failed.
	if notification.Status.CurrentState == appsv1alpha1.NotificationCompleted || notification.Status.CurrentState == appsv1alpha1.NotificationFailed {
		return ctrl.Result{}, nil
	}

	// Check if notification needs to be sent
	shouldSend, nextSendTime, err := r.shouldSendNotification(&notification)
	if err != nil {
		logger.Error(err, "Failed to determine if notification should be sent")
		r.updateStatusWithError(ctx, &notification, "EvaluationFailed", err.Error())
		return ctrl.Result{}, nil
	}

	// If it's not time to send yet, requeue at the next send time
	if !shouldSend && nextSendTime != nil {
		return ctrl.Result{RequeueAfter: time.Until(*nextSendTime)}, nil
	}

	if shouldSend {
		// Send the notification
		if err := r.sendNotification(ctx, &notification); err != nil {
			logger.Error(err, "Failed to send notification")

			updateNotificationStatus(&notification, appsv1alpha1.NotificationFailed, "JobCreationFailed", fmt.Sprintf("Failed to create delivery job: %v", err))
			if err := r.Status().Update(ctx, &notification); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}

		// Requeue to check job status later
		return ctrl.Result{RequeueAfter: time.Second * 10}, nil
	}

	return ctrl.Result{}, nil
}

// initializeStatusIfNeeded initializes the notification status if it's empty.
// Returns true if the status was initialized (indicating a requeue is needed), and an error indicating if something failed when checking or updating.
func (r *NotificationReconciler) initializeStatusIfNeeded(ctx context.Context, notification *appsv1alpha1.Notification) (bool, error) {
	if notification.Status.Conditions != nil {
		return false, nil
	}

	now := metav1.Now()
	notification.Status.Conditions = []appsv1alpha1.NotificationCondition{
		{
			Type:               appsv1alpha1.NotificationPending,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: now,
			Reason:             "Initializing",
			Message:            "Notification is being processed",
		},
	}
	notification.Status.DeliveryAttempts = make([]appsv1alpha1.DeliveryStatus, 0)
	notification.Status.CurrentState = appsv1alpha1.NotificationPending

	if err := r.Status().Update(ctx, notification); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update Notification status")
		return false, err
	}

	return true, nil
}

// shouldSendNotification determines if a notification should be sent now
// Returns:
// - boolean indicating if the notification should be sent
// - *time.Time pointer to next send time (if applicable)
// - error if evaluation fails
func (r *NotificationReconciler) shouldSendNotification(notification *appsv1alpha1.Notification) (bool, *time.Time, error) {
	// If never sent before, we should send it now.
	if notification.Status.LastSentTime == nil {
		return true, nil, nil
	}

	// If no schedule is defined, we only send once.
	if notification.Spec.Schedule == nil {
		return false, nil, nil
	}

	// Parse interval duration
	interval, err := time.ParseDuration(notification.Spec.Schedule.Interval)
	if err != nil {
		return false, nil, fmt.Errorf("invalid interval format: %w", err)
	}

	// Calculate next send time
	nextSendTime := notification.Status.LastSentTime.Add(interval)
	now := time.Now()

	// If we haven't reached the next send time yet
	if now.Before(nextSendTime) {
		return false, &nextSendTime, nil
	}

	// Check if we've hit the maximum repetitions
	maxRepetitions := getMaxRepetitionsOrDefault(notification, 1)
	if notification.Status.SentCount >= maxRepetitions {
		// We've reached our max repetitions
		return false, nil, nil
	}

	// We should send now
	return true, nil, nil
}

// Helper to get max repetitions or default value
func getMaxRepetitionsOrDefault(notification *appsv1alpha1.Notification, defaultValue int) int {
	if notification.Spec.Schedule != nil && notification.Spec.Schedule.MaxRepetitions != nil {
		return *notification.Spec.Schedule.MaxRepetitions
	}

	return defaultValue
}

// SetupWithManager sets up the controller with the Manager.
func (r *NotificationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&appsv1alpha1.Notification{}).
		Named("notification").
		Watches(
			&batchv1.Job{},
			handler.EnqueueRequestsFromMapFunc(r.jobToNotification),
		).
		Complete(r)
}

// Map jobs to the notifications that created them
func (r *NotificationReconciler) jobToNotification(ctx context.Context, obj client.Object) []reconcile.Request {
	job, ok := obj.(*batchv1.Job)
	if !ok {
		return nil
	}

	// Find the notification that owns this job via labels
	if notificationName, ok := job.Labels["notification"]; ok {
		notificationNamespace := job.Labels["notification-namespace"]
		if notificationNamespace == "" {
			notificationNamespace = job.Namespace
		}

		return []reconcile.Request{
			{
				NamespacedName: types.NamespacedName{
					Name:      notificationName,
					Namespace: notificationNamespace,
				},
			},
		}
	}

	return nil
}

// updateStatusWithError updates notification status to indicate an error
func (r *NotificationReconciler) updateStatusWithError(ctx context.Context, notification *appsv1alpha1.Notification, reason string, message string) {
	updateNotificationStatus(notification, appsv1alpha1.NotificationFailed, reason, message)

	if err := r.Status().Update(ctx, notification); err != nil {
		log.FromContext(ctx).Error(err, "Failed to update notification status with error")
	}
}

// Helper to update the notification status and maintain history
func updateNotificationStatus(notification *appsv1alpha1.Notification, state appsv1alpha1.NotificationStateType, reason string, message string) {
	now := metav1.Now()

	// Create a new condition for this update
	newCondition := appsv1alpha1.NotificationCondition{
		Type:               state,
		Status:             metav1.ConditionTrue,
		LastTransitionTime: now,
		Reason:             reason,
		Message:            message,
	}

	// Always add to the history of conditions
	notification.Status.Conditions = append(notification.Status.Conditions, newCondition)

	// Update the current state
	notification.Status.CurrentState = state
}

// sendNotification is responsible for sending the notification to the appropriate destination
func (r *NotificationReconciler) sendNotification(ctx context.Context, notification *appsv1alpha1.Notification) error {
	logger := log.FromContext(ctx)
	logger.Info("Processing notification", "name", notification.Name, "namespace", notification.Namespace)

	// Determine notification type and delegate to the appropriate handler
	if notification.Spec.Destination.Email != nil {
		return r.sendEmailNotification(notification)
	} else if notification.Spec.Destination.Slack != nil {
		return r.sendSlackNotification(notification)
	} else if notification.Spec.Destination.Webhook != nil {
		return r.sendWebhookNotification(ctx, notification)
	}

	return fmt.Errorf("no valid destination configured")
}

// sendEmailNotification handles email notifications
func (r *NotificationReconciler) sendEmailNotification(notification *appsv1alpha1.Notification) error {
	email := notification.Spec.Destination.Email

	log.Log.Info("Sending email notification",
		"to", email.To,
		"subject", email.Subject,
		"from", email.From,
		"body", notification.Spec.Body)

	// TODO: Implement actual email sending logic
	// Example:
	// return r.emailClient.Send(email.To, email.Subject, notification.Spec.Body, email.From)

	return nil
}

// sendSlackNotification handles Slack notifications
func (r *NotificationReconciler) sendSlackNotification(notification *appsv1alpha1.Notification) error {
	slack := notification.Spec.Destination.Slack

	log.Log.Info("Sending Slack notification",
		"channel", slack.Channel,
		"webhookUrl", slack.WebhookURL,
		"body", notification.Spec.Body)

	// TODO: Implement actual Slack sending logic
	// Example:
	// return r.slackClient.PostMessage(slack.Channel, notification.Spec.Body, slack.WebhookURL)

	return nil
}
