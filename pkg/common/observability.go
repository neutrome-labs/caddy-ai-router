package common

import (
	"os"

	"github.com/posthog/posthog-go"
)

var posthogClient posthog.Client

func TryInstrumentAppObservability() {
	var err error
	posthogClient, err = posthog.NewWithConfig(os.Getenv("POSTHOG_API_KEY"), posthog.Config{Endpoint: os.Getenv("POSTHOG_BASE_URL")})
	if err != nil {
		return // If we can't create the client, we just skip instrumentation
	}

	// defer client.Close()
}

func FireObservabilityEvent(userId, eventName string, properties map[string]interface{}) error {
	if posthogClient == nil {
		return nil
	}

	if userId == "" {
		userId = "unknown"
	}

	return posthogClient.Enqueue(posthog.Capture{
		DistinctId: userId,
		Event:      eventName,
		Properties: properties,
	})
}
