package controller

import (
	"context"
	"fmt"
	"time"

	appsv1alpha1 "github.com/giornetta/kube-messaging/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// checkJobStatus checks the delivery status of jobs created by this operator.
// Add the checkJobStatus function
func (r *NotificationReconciler) checkJobStatus(ctx context.Context, notification *appsv1alpha1.Notification) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Only process if we're not already in a terminal state
	if notification.Status.CurrentState == appsv1alpha1.NotificationFailed ||
		notification.Status.CurrentState == appsv1alpha1.NotificationCompleted {
		return ctrl.Result{}, nil
	}

	// Look for jobs owned by this notification
	var jobList batchv1.JobList
	err := r.List(ctx, &jobList,
		client.MatchingLabels{
			"app":          "notification-operator",
			"notification": notification.Name,
		})
	if err != nil {
		logger.Error(err, "Failed to list jobs")
		return ctrl.Result{}, err
	}

	updated := false

	// Check each job's status
	for _, job := range jobList.Items {
		// Skip jobs we've already processed
		jobProcessed := false
		for _, attempt := range notification.Status.DeliveryAttempts {
			if attempt.JobName == job.Name {
				jobProcessed = true
				break
			}
		}
		if jobProcessed {
			continue
		}

		// Only process completed jobs (succeeded or failed after all retries)
		backoffLimit := int32(3) // Default
		if job.Spec.BackoffLimit != nil {
			backoffLimit = *job.Spec.BackoffLimit
		}

		jobFinished := job.Status.Succeeded > 0 ||
			(job.Status.Failed > 0 && job.Status.Failed >= backoffLimit)

		if jobFinished {
			now := metav1.Now()
			success := job.Status.Succeeded > 0

			// Create delivery status record
			deliveryStatus := appsv1alpha1.DeliveryStatus{
				Time:    now,
				JobName: job.Name,
				Success: success,
			}

			if !success {
				// Job failed after all retries
				deliveryStatus.Error = "Job failed after all retry attempts"

				// Fetch the pod to get error details if possible
				podList := &corev1.PodList{}
				err := r.List(ctx, podList,
					client.InNamespace(job.Namespace),
					client.MatchingLabels(job.Spec.Selector.MatchLabels))

				if err == nil && len(podList.Items) > 0 {
					pod := podList.Items[len(podList.Items)-1]
					if len(pod.Status.ContainerStatuses) > 0 {
						containerStatus := pod.Status.ContainerStatuses[0]
						if containerStatus.State.Terminated != nil {
							deliveryStatus.Error = containerStatus.State.Terminated.Message
						}
					}
				}

				// Record this delivery attempt
				notification.Status.DeliveryAttempts = append(notification.Status.DeliveryAttempts, deliveryStatus)

				// Mark notification as failed after job failure
				updateNotificationStatus(notification, appsv1alpha1.NotificationFailed, "DeliveryFailed", "Webhook delivery failed after all retry attempts")
				updated = true
			} else {
				// Job succeeded
				notification.Status.DeliveryAttempts = append(notification.Status.DeliveryAttempts, deliveryStatus)
				notification.Status.LastSentTime = &now
				notification.Status.SentCount++

				// Determine if this was the final delivery needed
				maxRepetitions := getMaxRepetitionsOrDefault(notification, 1)
				isFinalDelivery := notification.Spec.Schedule == nil ||
					notification.Status.SentCount >= maxRepetitions

				if isFinalDelivery {
					// All deliveries completed successfully
					updateNotificationStatus(notification, appsv1alpha1.NotificationCompleted,
						"AllDeliveriesCompleted", fmt.Sprintf("All deliveries completed successfully (%d/%d)",
							notification.Status.SentCount, maxRepetitions))
				} else {
					// More deliveries scheduled
					updateNotificationStatus(notification, appsv1alpha1.NotificationReady,
						"DeliverySucceeded", fmt.Sprintf("Delivery succeeded (%d/%d)",
							notification.Status.SentCount, maxRepetitions))
				}

				updated = true
			}

			logger.Info("Updated delivery status",
				"job", job.Name,
				"success", deliveryStatus.Success,
				"notificationState", notification.Status.CurrentState)
		}
	}

	// Update status if changed
	if updated {
		err := r.Status().Update(ctx, notification)
		if err != nil {
			logger.Error(err, "Failed to update notification status")
			return ctrl.Result{}, err
		}
	}

	// If any jobs are still active, requeue to check again later
	for _, job := range jobList.Items {
		if job.Status.Active > 0 {
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
	}

	return ctrl.Result{}, nil
}
