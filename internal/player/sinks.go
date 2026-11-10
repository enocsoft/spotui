// Real PulseAudio sink listing for the output picker (spec S16): `d` lists
// the audio outputs of this machine, never Spotify devices.
package player

import (
	"fmt"

	"github.com/jfreymuth/pulse"

	"github.com/enocsoft/spotui/internal/library"
)

// listPulseSinks connects to the local PulseAudio/PipeWire server and lists
// every sink with its id, display label and the default sink id.
func listPulseSinks() ([]library.Sink, string, error) {
	client, err := pulse.NewClient()
	if err != nil {
		return nil, "", fmt.Errorf("pulseaudio: %w", err)
	}
	defer client.Close()

	list, err := client.ListSinks()
	if err != nil {
		return nil, "", fmt.Errorf("pulseaudio sinks: %w", err)
	}
	defaultID := ""
	if def, defErr := client.DefaultSink(); defErr == nil {
		defaultID = def.ID()
	}

	sinks := make([]library.Sink, 0, len(list))
	for _, sk := range list {
		sinks = append(sinks, library.Sink{
			ID:   sk.ID(),
			Name: SinkLabel(sk.ID(), sk.Name()),
		})
	}
	return sinks, defaultID, nil
}
