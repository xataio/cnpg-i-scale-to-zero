package plugin

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunPluginStopsServerWhenScraperStops(t *testing.T) {
	t.Parallel()

	scraperErr := errors.New("scraper stopped")
	serverStopped := false

	err := runPlugin(
		context.Background(),
		func(ctx context.Context) error {
			<-ctx.Done()
			serverStopped = true
			return nil
		},
		func(context.Context) error {
			return scraperErr
		},
	)

	require.ErrorIs(t, err, scraperErr)
	require.True(t, serverStopped)
}
