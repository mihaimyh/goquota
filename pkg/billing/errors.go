package billing

import "errors"

var (
	// ErrProviderNotConfigured is returned when a provider is not properly configured
	ErrProviderNotConfigured = errors.New("billing provider not configured")

	// ErrInvalidWebhookSignature is returned when webhook signature validation fails
	ErrInvalidWebhookSignature = errors.New("invalid webhook signature")

	// ErrInvalidWebhookPayload is returned when webhook payload cannot be parsed
	ErrInvalidWebhookPayload = errors.New("invalid webhook payload")

	// ErrUserNotFound is returned when a user cannot be found in the provider's system
	ErrUserNotFound = errors.New("user not found in billing provider")

	// ErrProviderAPIError is returned when the provider's API returns an error
	ErrProviderAPIError = errors.New("billing provider API error")
)
