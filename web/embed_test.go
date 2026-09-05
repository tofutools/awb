package web

import (
	"encoding/json"
	"image/png"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMobileWebAppManifest(t *testing.T) {
	static, err := StaticFS()
	require.NoError(t, err)

	body, err := fs.ReadFile(static, "manifest.json")
	require.NoError(t, err)
	var manifest struct {
		Name     string `json:"name"`
		ID       string `json:"id"`
		StartURL string `json:"start_url"`
		Scope    string `json:"scope"`
		Display  string `json:"display"`
		Icons    []struct {
			Source string `json:"src"`
			Sizes  string `json:"sizes"`
			Type   string `json:"type"`
		} `json:"icons"`
	}
	require.NoError(t, json.Unmarshal(body, &manifest))
	assert.Equal(t, "Agent Work Board", manifest.Name)
	assert.Equal(t, ".", manifest.ID)
	assert.Equal(t, ".", manifest.StartURL)
	assert.Equal(t, ".", manifest.Scope)
	assert.Equal(t, "standalone", manifest.Display)

	require.Len(t, manifest.Icons, 2)
	for _, icon := range manifest.Icons {
		assert.Equal(t, "image/png", icon.Type)
		file, err := static.Open(icon.Source)
		require.NoError(t, err, icon.Source)
		config, err := png.DecodeConfig(file)
		require.NoError(t, err, icon.Source)
		require.NoError(t, file.Close())
		dimensions := strings.Split(icon.Sizes, "x")
		require.Len(t, dimensions, 2, icon.Sizes)
		width, err := strconv.Atoi(dimensions[0])
		require.NoError(t, err, icon.Sizes)
		height, err := strconv.Atoi(dimensions[1])
		require.NoError(t, err, icon.Sizes)
		assert.Equal(t, width, config.Width, icon.Source)
		assert.Equal(t, height, config.Height, icon.Source)
	}
	assert.Equal(t, "192x192", manifest.Icons[0].Sizes)
	assert.Equal(t, "512x512", manifest.Icons[1].Sizes)

	shell, err := Shell("/awb/")
	require.NoError(t, err)
	assert.Contains(t, string(shell), `<link rel="manifest" href="manifest.json" crossorigin="use-credentials">`)
	assert.Contains(t, string(shell), `<link rel="apple-touch-icon" sizes="192x192" href="awb-mark-192.png">`)
}
