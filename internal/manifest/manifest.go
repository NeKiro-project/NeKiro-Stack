package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

const SchemaVersion = "1"

var (
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	tagPattern      = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.-]+)?$`)
	contractPattern = regexp.MustCompile(`^NeKiro/contracts@[0-9a-f]{40}$`)
)

type Manifest struct {
	SchemaVersion    string     `json:"schemaVersion"`
	ContractIdentity string     `json:"contractIdentity"`
	Components       Components `json:"components"`
}

type Components struct {
	Core        Component `json:"core"`
	Console     Component `json:"console"`
	SDKGo       Component `json:"sdkGo"`
	Samples     Component `json:"samples"`
	TransportGo Component `json:"transportGo"`
}

type Component struct {
	Repository string           `json:"repository"`
	CommitSHA  string           `json:"commitSha"`
	Tag        string           `json:"tag,omitempty"`
	Images     map[string]Image `json:"images,omitempty"`
}

type Image struct {
	Reference string `json:"reference"`
	Digest    string `json:"digest"`
}

type NamedComponent struct {
	Name      string
	Component Component
}

type Resolution struct {
	CommitSHA    string
	TagCommitSHA string
	ImageDigests map[string]string
}

func LoadFile(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read component manifest: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (Manifest, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return Manifest{}, errors.New("component manifest is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value Manifest
	if err := decoder.Decode(&value); err != nil {
		return Manifest{}, fmt.Errorf("decode component manifest: %w", err)
	}
	if err := requireEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := value.Validate(); err != nil {
		return Manifest{}, err
	}
	return value, nil
}

func (value Manifest) Validate() error {
	if value.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schemaVersion must equal %q", SchemaVersion)
	}
	if !contractPattern.MatchString(value.ContractIdentity) {
		return errors.New("contractIdentity must be NeKiro/contracts@ followed by one full commit SHA")
	}
	if strings.TrimPrefix(value.ContractIdentity, "NeKiro/contracts@") != value.Components.Core.CommitSHA {
		return errors.New("contractIdentity commit must equal the core component commit")
	}
	expected := map[string]string{
		"core":        "NeKiro-project/NeKiro",
		"console":     "NeKiro-project/NeKiro-Console",
		"sdkGo":       "NeKiro-project/nekiro-sdk-go",
		"samples":     "NeKiro-project/NeKiro-Samples",
		"transportGo": "NeKiro-project/nekiro-a2a-transport-go",
	}
	for _, item := range value.Ordered() {
		component := item.Component
		if component.Repository != expected[item.Name] {
			return fmt.Errorf("component %s repository must equal %s", item.Name, expected[item.Name])
		}
		if !commitPattern.MatchString(component.CommitSHA) {
			return fmt.Errorf("component %s commitSha must be one full lowercase commit SHA", item.Name)
		}
		if component.Tag != "" {
			if !tagPattern.MatchString(component.Tag) || floating(component.Tag) {
				return fmt.Errorf("component %s tag is not an immutable semantic version", item.Name)
			}
		}
		imageNames := make([]string, 0, len(component.Images))
		for name := range component.Images {
			imageNames = append(imageNames, name)
		}
		sort.Strings(imageNames)
		for _, name := range imageNames {
			image := component.Images[name]
			if strings.TrimSpace(name) == "" || image.Reference == "" || strings.Contains(image.Reference, "@latest") || strings.HasSuffix(image.Reference, ":latest") {
				return fmt.Errorf("component %s image %q has an invalid or floating reference", item.Name, name)
			}
			if !digestPattern.MatchString(image.Digest) {
				return fmt.Errorf("component %s image %q digest must be an exact sha256 digest", item.Name, name)
			}
		}
	}
	if value.Components.TransportGo.Tag == "" {
		return errors.New("transportGo tag is required for the published transport release")
	}
	return nil
}

func (value Manifest) Ordered() []NamedComponent {
	return []NamedComponent{
		{Name: "core", Component: value.Components.Core},
		{Name: "console", Component: value.Components.Console},
		{Name: "sdkGo", Component: value.Components.SDKGo},
		{Name: "samples", Component: value.Components.Samples},
		{Name: "transportGo", Component: value.Components.TransportGo},
	}
}

func (value Manifest) ValidateResolution(resolved map[string]Resolution) error {
	for _, item := range value.Ordered() {
		resolution, ok := resolved[item.Name]
		if !ok {
			return fmt.Errorf("component %s resolution is missing", item.Name)
		}
		if resolution.CommitSHA != item.Component.CommitSHA {
			return fmt.Errorf("component %s resolved commit does not match manifest", item.Name)
		}
		if item.Component.Tag != "" && resolution.TagCommitSHA != item.Component.CommitSHA {
			return fmt.Errorf("component %s tag does not resolve to the manifest commit", item.Name)
		}
		for name, image := range item.Component.Images {
			if resolution.ImageDigests[name] != image.Digest {
				return fmt.Errorf("component %s image %q digest does not match manifest", item.Name, name)
			}
		}
	}
	return nil
}

func floating(value string) bool {
	switch strings.ToLower(value) {
	case "main", "master", "head", "latest":
		return true
	default:
		return false
	}
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("component manifest contains trailing JSON")
		}
		return fmt.Errorf("decode trailing component manifest data: %w", err)
	}
	return nil
}
