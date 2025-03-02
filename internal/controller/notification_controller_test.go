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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1alpha1 "github.com/giornetta/kube-messaging/api/v1alpha1"
)

var _ = Describe("Notification Controller", func() {
	// Constants for testing
	const (
		NotificationName      = "test-notification"
		NotificationNamespace = "default"
		WebhookImageName      = "test-webhook-image:latest"
	)

	// Helper variables
	var (
		ctx                context.Context
		reconciler         NotificationReconciler
		req                reconcile.Request
		notificationLookup types.NamespacedName
	)

	// Helper functions
	getTestNotification := func(withWebhook bool) *appsv1alpha1.Notification {
		notification := &appsv1alpha1.Notification{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "app.kube-messaging.michelegiornetta.com/v1alpha1",
				Kind:       "Notification",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      NotificationName,
				Namespace: NotificationNamespace,
			},
			Spec: appsv1alpha1.NotificationSpec{
				Body: "Test notification message",
			},
		}

		if withWebhook {
			notification.Spec.Destination = appsv1alpha1.Destination{
				Webhook: &appsv1alpha1.WebhookDestination{
					URL: "https://example.com/webhook",
					Headers: map[string]string{
						"X-Custom-Header": "test-value",
					},
				},
			}
		}

		return notification
	}

	getScheduledNotification := func() *appsv1alpha1.Notification {
		notification := getTestNotification(true)
		maxRepetitions := 3
		notification.Spec.Schedule = &appsv1alpha1.Schedule{
			Interval:       "1h",
			MaxRepetitions: &maxRepetitions,
		}
		return notification
	}

	// Setup before each test
	BeforeEach(func() {
		ctx = context.Background()
		notificationLookup = types.NamespacedName{
			Name:      NotificationName,
			Namespace: NotificationNamespace,
		}
		req = reconcile.Request{
			NamespacedName: notificationLookup,
		}

		// Create a new reconciler for each test
		reconciler = NotificationReconciler{
			Client:       k8sClient,
			Scheme:       k8sClient.Scheme(),
			WebhookImage: WebhookImageName,
		}
	})

	// Cleanup after each test
	AfterEach(func() {
		// Delete any notification resources that were created
		notification := &appsv1alpha1.Notification{}
		err := k8sClient.Get(ctx, notificationLookup, notification)
		if err == nil {
			Expect(k8sClient.Delete(ctx, notification)).To(Succeed())
		}

		// Delete any jobs that were created
		bgOption := metav1.DeletePropagationBackground
		jobList := &batchv1.JobList{}
		err = k8sClient.List(ctx, jobList, client.InNamespace(NotificationNamespace))
		if err == nil {
			for _, job := range jobList.Items {
				Expect(k8sClient.Delete(ctx, &job, &client.DeleteOptions{
					PropagationPolicy: &bgOption,
				})).To(Succeed())
			}
		}
	})

	Context("Notification status initialization", func() {
		It("should initialize status when reconciling a new notification", func() {
			// Create a new notification without status
			notification := getTestNotification(true)
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify status was initialized
			updatedNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, updatedNotification)).To(Succeed())
			Expect(updatedNotification.Status.Conditions).NotTo(BeEmpty())
			Expect(updatedNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationPending))
		})
	})

	Context("Webhook notification reconciliation", func() {
		It("should create a job when reconciling a webhook notification", func() {
			// Create a webhook notification
			notification := getTestNotification(true)
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// First reconcile to initialize status
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile to create the job
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify job was created
			jobList := &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"app":               "notification-operator",
					"notification":      NotificationName,
					"notification-type": "webhook",
				})).To(Succeed())

			Expect(jobList.Items).To(HaveLen(1))
			job := jobList.Items[0]

			// Verify job properties
			Expect(job.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(job.Spec.Template.Spec.Containers[0].Image).To(Equal(WebhookImageName))

			// Verify owner reference
			Expect(job.OwnerReferences).To(HaveLen(1))
			Expect(job.OwnerReferences[0].Name).To(Equal(NotificationName))
		})

		It("should handle job completion and update notification status", func() {
			// Create a webhook notification
			notification := getTestNotification(true)
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// First reconcile to initialize status
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile to create the job
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Find the created job
			jobList := &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())
			Expect(jobList.Items).To(HaveLen(1))

			job := &jobList.Items[0]

			// Update job status to indicate success
			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			// Reconcile to process job completion
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify notification status update
			updatedNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, updatedNotification)).To(Succeed())

			Expect(updatedNotification.Status.SentCount).To(Equal(1))
			Expect(updatedNotification.Status.LastSentTime).NotTo(BeNil())
			Expect(updatedNotification.Status.DeliveryAttempts).To(HaveLen(1))
			Expect(updatedNotification.Status.DeliveryAttempts[0].Success).To(BeTrue())
			Expect(updatedNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationCompleted))
		})

		It("should handle job failure and update notification status", func() {
			// Create a webhook notification
			notification := getTestNotification(true)
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// Reconcile to initialize and create job
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Find the created job
			jobList := &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())

			job := &jobList.Items[0]

			// Update job status to indicate failure (failed 3 times with backoffLimit=3)
			job.Status.Failed = 3
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			// Reconcile to process job failure
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify notification status update
			updatedNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, updatedNotification)).To(Succeed())

			Expect(updatedNotification.Status.DeliveryAttempts).To(HaveLen(1))
			Expect(updatedNotification.Status.DeliveryAttempts[0].Success).To(BeFalse())
			Expect(updatedNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationFailed))
		})
	})

	Context("Scheduled notifications", func() {
		It("should handle recurring notifications with scheduling", func() {
			// Create a scheduled notification
			notification := getScheduledNotification()
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// First reconcile to initialize status
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile to create the job
			result, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeFalse())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			// Find the created job
			jobList := &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())

			job := &jobList.Items[0]

			// Update job status to indicate success
			job.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, job)).To(Succeed())

			// Reconcile to process job completion
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify notification status - should be Ready for next delivery
			updatedNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, updatedNotification)).To(Succeed())

			Expect(updatedNotification.Status.SentCount).To(Equal(1))
			Expect(updatedNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationReady))

			// Manually set LastSentTime to an hour ago to trigger next delivery
			now := metav1.NewTime(time.Now().Add(-65 * time.Minute))
			updatedNotification.Status.LastSentTime = &now
			Expect(k8sClient.Status().Update(ctx, updatedNotification)).To(Succeed())

			// Reconcile again to create second job
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify second job was created
			jobList = &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())

			// Should have 2 jobs now (first one and second one)
			Expect(jobList.Items).To(HaveLen(2))
		})

		It("should complete after max repetitions", func() {
			// Create a scheduled notification with max 2 repetitions
			notification := getScheduledNotification()
			maxRepetitions := 2
			notification.Spec.Schedule.MaxRepetitions = &maxRepetitions
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// Reconcile to initialize and create first job
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Find and update job to succeed
			jobList := &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())

			job1 := &jobList.Items[0]
			job1.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, job1)).To(Succeed())

			// Reconcile to process completion
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify notification status
			updatedNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, updatedNotification)).To(Succeed())
			Expect(updatedNotification.Status.SentCount).To(Equal(1))
			Expect(updatedNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationReady))

			// Set time to trigger second delivery
			now := metav1.NewTime(time.Now().Add(-65 * time.Minute))
			updatedNotification.Status.LastSentTime = &now
			Expect(k8sClient.Status().Update(ctx, updatedNotification)).To(Succeed())

			// Reconcile to create second job
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Find and update second job to succeed
			jobList = &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())

			// Get the second job (should be jobList.Items[1])
			Expect(jobList.Items).To(HaveLen(2))
			job2 := &jobList.Items[1]
			job2.Status.Succeeded = 1
			Expect(k8sClient.Status().Update(ctx, job2)).To(Succeed())

			// Reconcile to process completion
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify notification is now completed after reaching max repetitions
			finalNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, finalNotification)).To(Succeed())
			Expect(finalNotification.Status.SentCount).To(Equal(2))
			Expect(finalNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationCompleted))
		})
	})

	Context("Error handling", func() {
		It("should handle unsupported notification destinations", func() {
			// Create notification with email destination (which is not implemented)
			notification := getTestNotification(false)
			notification.Spec.Destination = appsv1alpha1.Destination{
				Email: &appsv1alpha1.EmailDestination{
					To:      []string{"test@example.com"},
					Subject: "Test Email",
				},
			}
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile should mark it as failed
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify notification status - should be Failed
			updatedNotification := &appsv1alpha1.Notification{}
			Expect(k8sClient.Get(ctx, notificationLookup, updatedNotification)).To(Succeed())

			Expect(updatedNotification.Status.CurrentState).To(Equal(appsv1alpha1.NotificationFailed))
			Expect(updatedNotification.Status.Conditions).To(ContainElement(
				WithTransform(func(c appsv1alpha1.NotificationCondition) string {
					return c.Reason
				}, Equal("DestinationUnimplemented")),
			))
		})
	})

	Context("Lifecycle testing", func() {
		It("should not reconcile notifications in terminal states", func() {
			// Create a notification and mark it as completed
			notification := getTestNotification(true)
			notification.Status = appsv1alpha1.NotificationStatus{
				CurrentState: appsv1alpha1.NotificationCompleted,
				Conditions: []appsv1alpha1.NotificationCondition{
					{
						Type:               appsv1alpha1.NotificationCompleted,
						Status:             metav1.ConditionTrue,
						LastTransitionTime: metav1.Now(),
						Reason:             "Completed",
						Message:            "Notification completed",
					},
				},
				SentCount: 1,
			}
			Expect(k8sClient.Create(ctx, notification)).To(Succeed())

			// Status won't be written without an explicit call to Status().Update
			Expect(k8sClient.Status().Update(ctx, notification)).To(Succeed())

			// Reconcile
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			// Verify no jobs created for completed notification
			jobList := &batchv1.JobList{}
			Expect(k8sClient.List(ctx, jobList,
				client.InNamespace(NotificationNamespace),
				client.MatchingLabels{
					"notification": NotificationName,
				})).To(Succeed())

			Expect(jobList.Items).To(BeEmpty())
		})
	})
})
