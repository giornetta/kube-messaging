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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NotificationSpec defines the desired state of Notification.
type NotificationSpec struct {
	// Body is the content of the notification message
	// +kubebuilder:validation:Required
	Body string `json:"body"`

	// Destination specifies where the notification will be sent
	// +kubebuilder:validation:Required
	Destination Destination `json:"destination"`

	// Schedule is an optional configuration for repeating notifications
	// +optional
	Schedule *Schedule `json:"schedule,omitempty"`
}

// Destination defines the various destinations where notifications can be sent
type Destination struct {
	// Email configuration for sending notifications via email
	// +optional
	Email *EmailDestination `json:"email,omitempty"`

	// Slack configuration for sending notifications to Slack
	// +optional
	Slack *SlackDestination `json:"slack,omitempty"`

	// Webhook configuration for sending notifications to a webhook endpoint
	// +optional
	Webhook *WebhookDestination `json:"webhook,omitempty"`
}

// EmailDestination defines configuration for email notifications
type EmailDestination struct {
	// To is a list of email addresses to send the notification to
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	To []string `json:"to"`

	// Subject is the email subject line
	// +optional
	Subject string `json:"subject,omitempty"`

	// From is the sender's email address
	// +optional
	From string `json:"from,omitempty"`
}

// SlackDestination defines configuration for Slack notifications
type SlackDestination struct {
	// Channel is the Slack channel to send the notification to
	// +kubebuilder:validation:Required
	Channel string `json:"channel"`

	// WebhookURL is the Slack webhook URL
	// +optional
	WebhookURL string `json:"webhookUrl,omitempty"`
}

// WebhookDestination defines configuration for webhook notifications
type WebhookDestination struct {
	// URL is the webhook endpoint to send the notification to
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=uri
	URL string `json:"url"`

	// Headers are optional HTTP headers to include in the webhook request
	// +optional
	Headers map[string]string `json:"headers,omitempty"`
}

// Schedule defines the configuration for repeating notifications
type Schedule struct {
	// Interval specifies how often to repeat the notification in duration format (e.g. 1h, 30m, 24h)
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=^([0-9]+(h|m|s))+$
	Interval string `json:"interval"`

	// MaxRepetitions specifies the maximum number of times to repeat the notification
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxRepetitions *int `json:"maxRepetitions,omitempty"`
}

// Add a new type for the current state
// +kubebuilder:validation:Enum=Pending;Ready;Completed;Failed
type NotificationStateType string

const (
	NotificationPending   NotificationStateType = "Pending"
	NotificationReady     NotificationStateType = "Ready"
	NotificationCompleted NotificationStateType = "Completed"
	NotificationFailed    NotificationStateType = "Failed"
)

// NotificationCondition defines the observed state of a Notification condition
type NotificationCondition struct {
	// Type of the condition
	Type NotificationStateType `json:"type"`

	// Status of the condition, one of True, False, Unknown
	Status metav1.ConditionStatus `json:"status"`

	// Reason contains a programmatic identifier indicating the reason for the condition's state
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message is a human-readable message indicating details about the transition
	// +optional
	Message string `json:"message,omitempty"`

	// LastTransitionTime is the last time the condition transitioned from one status to another
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty"`
}

// DeliveryStatus defines the status of delivery attempts for notifications.
type DeliveryStatus struct {
	// Time when delivery was attempted
	Time metav1.Time `json:"time"`

	// Success indicates whether the delivery was successful
	Success bool `json:"success"`

	// HTTP status code from the webhook response
	StatusCode int `json:"statusCode,omitempty"`

	// Error message if delivery failed
	Error string `json:"error,omitempty"`

	// JobName references the Kubernetes job that handled the delivery
	JobName string `json:"jobName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// NotificationStatus defines the observed state of Notification
type NotificationStatus struct {
	// CurrentState represents the active status of the notification
	// +optional
	CurrentState NotificationStateType `json:"currentState,omitempty"`

	// Conditions represent the latest available observations of the notification's state
	// +optional
	Conditions []NotificationCondition `json:"conditions,omitempty"`

	// LastSentTime is the time when the notification was last sent
	// +optional
	LastSentTime *metav1.Time `json:"lastSentTime,omitempty"`

	// SentCount is the number of times the notification has been sent
	// +optional
	SentCount int `json:"sentCount,omitempty"`

	// DeliveryStatus contains information about delivery attempts
	// +optional
	DeliveryAttempts []DeliveryStatus `json:"deliveryAttempts,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.currentState"

// Notification is the Schema for the notifications API.
type Notification struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NotificationSpec   `json:"spec,omitempty"`
	Status NotificationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NotificationList contains a list of Notification.
type NotificationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Notification `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Notification{}, &NotificationList{})
}
