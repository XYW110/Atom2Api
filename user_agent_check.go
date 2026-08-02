package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

const (
	atomCodeCargoURL        = "https://raw.atomgit.com/atomgit_atomcode/atomcode/raw/main/Cargo.toml"
	atomCodeManifestMaxSize = 1 << 20
)

type userAgentCheckResponse struct {
	Version   string `json:"version"`
	UserAgent string `json:"user_agent"`
}

type atomCodeManifest struct {
	Workspace struct {
		Package struct {
			Version string `toml:"version"`
		} `toml:"package"`
	} `toml:"workspace"`
}

type UserAgentChecker struct {
	manifestURL string
	client      *http.Client
}

func NewUserAgentChecker() *UserAgentChecker {
	return newUserAgentChecker(atomCodeCargoURL, &http.Client{Timeout: 10 * time.Second})
}

func newUserAgentChecker(manifestURL string, client *http.Client) *UserAgentChecker {
	return &UserAgentChecker{manifestURL: manifestURL, client: client}
}

func (c *UserAgentChecker) HandleCheck(w http.ResponseWriter, r *http.Request) {
	result, err := c.Check(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, errorResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (c *UserAgentChecker) Check(ctx context.Context) (userAgentCheckResponse, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.manifestURL, nil)
	if err != nil {
		return userAgentCheckResponse{}, fmt.Errorf("create AtomCode manifest request: %w", err)
	}
	request.Header.Set("Accept", "text/plain")
	request.Header.Set("User-Agent", "Atom2Api/user-agent-check")

	response, err := c.client.Do(request)
	if err != nil {
		return userAgentCheckResponse{}, fmt.Errorf("fetch AtomCode manifest: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		return userAgentCheckResponse{}, fmt.Errorf("AtomCode manifest request returned %s", response.Status)
	}

	body, err := io.ReadAll(io.LimitReader(response.Body, atomCodeManifestMaxSize+1))
	if err != nil {
		return userAgentCheckResponse{}, fmt.Errorf("read AtomCode manifest: %w", err)
	}
	if len(body) > atomCodeManifestMaxSize {
		return userAgentCheckResponse{}, fmt.Errorf("AtomCode manifest exceeds %d bytes", atomCodeManifestMaxSize)
	}

	var manifest atomCodeManifest
	if err := toml.Unmarshal(body, &manifest); err != nil {
		return userAgentCheckResponse{}, fmt.Errorf("parse AtomCode manifest: %w", err)
	}
	version := normalizeVersionLabel(strings.TrimSpace(manifest.Workspace.Package.Version))
	if _, ok := parseSemanticVersion(version); !ok {
		return userAgentCheckResponse{}, fmt.Errorf("AtomCode workspace package version is missing or invalid")
	}
	return userAgentCheckResponse{Version: version, UserAgent: "atomcode/" + version}, nil
}
