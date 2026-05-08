package pluginio

import (
	"encoding/json"
	"fmt"
	"io"
)

// Input is the subset of Stash raw-plugin input used by the engine.
// Stash commonly sends Args and PluginDir with capitalized keys, while tests
// and manual invocations often use lower-case JSON. UnmarshalJSON accepts both.
type Input struct {
	Args             map[string]any `json:"args,omitempty"`
	PluginDir        string         `json:"pluginDir,omitempty"`
	ServerConnection map[string]any `json:"serverConnection,omitempty"`
}

type inputAlias struct {
	ArgsLower             map[string]any `json:"args"`
	ArgsUpper             map[string]any `json:"Args"`
	PluginDirLower        string         `json:"pluginDir"`
	PluginDirUpper        string         `json:"PluginDir"`
	ServerConnectionLower map[string]any `json:"serverConnection"`
	ServerConnectionSnake map[string]any `json:"server_connection"`
	ServerConnectionUpper map[string]any `json:"ServerConnection"`
}

func (i *Input) UnmarshalJSON(data []byte) error {
	var raw inputAlias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	i.Args = raw.ArgsLower
	if i.Args == nil {
		i.Args = raw.ArgsUpper
	}
	if i.Args == nil {
		i.Args = map[string]any{}
	}
	i.PluginDir = raw.PluginDirLower
	if i.PluginDir == "" {
		i.PluginDir = raw.PluginDirUpper
	}
	i.ServerConnection = raw.ServerConnectionLower
	if i.ServerConnection == nil {
		i.ServerConnection = raw.ServerConnectionSnake
	}
	if i.ServerConnection == nil {
		i.ServerConnection = raw.ServerConnectionUpper
	}
	if i.PluginDir == "" {
		i.PluginDir = stringFromMap(i.ServerConnection, "PluginDir", "pluginDir")
	}
	return nil
}

func stringFromMap(values map[string]any, keys ...string) string {
	if values == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := values[key]; ok {
			if s, ok := value.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func ReadInput(r io.Reader) (Input, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Input{}, fmt.Errorf("read plugin input: %w", err)
	}
	if len(data) == 0 {
		return Input{Args: map[string]any{}}, nil
	}
	var input Input
	if err := json.Unmarshal(data, &input); err != nil {
		return Input{}, fmt.Errorf("decode plugin input: %w", err)
	}
	if input.Args == nil {
		input.Args = map[string]any{}
	}
	return input, nil
}

// OutputEnvelope is the raw-plugin response shape Stash accepts. Output is a
// JSON-encoded string so runPluginOperation consumers can parse it consistently
// with the existing JS dummy engine.
type OutputEnvelope struct {
	Output string `json:"Output"`
}

func MarshalOutput(payload any) ([]byte, error) {
	inner, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode plugin payload: %w", err)
	}
	outer, err := json.Marshal(OutputEnvelope{Output: string(inner)})
	if err != nil {
		return nil, fmt.Errorf("encode plugin envelope: %w", err)
	}
	return append(outer, '\n'), nil
}

func WriteOutput(w io.Writer, payload any) error {
	data, err := MarshalOutput(payload)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
