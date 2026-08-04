package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestValidManifest(t *testing.T) {
	value := validManifest()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Components.Core.CommitSHA != value.Components.Core.CommitSHA {
		t.Fatal("validated manifest changed the core commit")
	}
}

func TestManifestCoreOverrideUpdatesCommitAndContractIdentity(t *testing.T) {
	value := validManifest()
	commitSHA := strings.Repeat("f", 40)
	overridden, err := value.WithCoreCommit(commitSHA)
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Components.Core.CommitSHA != commitSHA {
		t.Fatalf("Core commit = %q", overridden.Components.Core.CommitSHA)
	}
	if overridden.ContractIdentity != "NeKiro/contracts@"+commitSHA {
		t.Fatalf("contract identity = %q", overridden.ContractIdentity)
	}
	if overridden.Components.Samples.CommitSHA != value.Components.Samples.CommitSHA {
		t.Fatal("Core override changed a satellite component")
	}
}

func TestManifestCoreOverrideRejectsNonCommitReference(t *testing.T) {
	if _, err := validManifest().WithCoreCommit("main"); err == nil {
		t.Fatal("floating Core override was accepted")
	}
}

func TestManifestRejectsMissingMalformedFloatingAndLocalComponents(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{name: "missing", mutate: func(value *Manifest) { value.Components.SDKGo = Component{} }},
		{name: "malformed sha", mutate: func(value *Manifest) { value.Components.Core.CommitSHA = "abc123" }},
		{name: "floating tag", mutate: func(value *Manifest) { value.Components.TransportGo.Tag = "latest" }},
		{name: "local repository", mutate: func(value *Manifest) { value.Components.Console.Repository = "../console" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validManifest()
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestManifestRejectsUnknownBranchField(t *testing.T) {
	data, err := json.Marshal(validManifest())
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Replace(string(data), `"commitSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"`, `"commitSha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","branch":"main"`, 1)
	if _, err := Parse([]byte(text)); err == nil {
		t.Fatal("unknown floating branch field was accepted")
	}
}

func TestResolutionRejectsRetaggedAndDigestMismatchedComponents(t *testing.T) {
	value := validManifest()
	value.Components.Core.Images = map[string]Image{
		"controlPlane": {Reference: "ghcr.io/nekiro-project/control-plane", Digest: "sha256:" + strings.Repeat("a", 64)},
	}
	resolved := validResolutions(value)
	resolved["transportGo"] = Resolution{CommitSHA: value.Components.TransportGo.CommitSHA, TagCommitSHA: strings.Repeat("f", 40)}
	if err := value.ValidateResolution(resolved); err == nil {
		t.Fatal("retagged transport release was accepted")
	}
	resolved = validResolutions(value)
	resolved["core"] = Resolution{CommitSHA: value.Components.Core.CommitSHA, ImageDigests: map[string]string{"controlPlane": "sha256:" + strings.Repeat("b", 64)}}
	if err := value.ValidateResolution(resolved); err == nil {
		t.Fatal("mismatched image digest was accepted")
	}
}

func validManifest() Manifest {
	return Manifest{
		SchemaVersion:    SchemaVersion,
		ContractIdentity: "NeKiro/contracts@" + strings.Repeat("a", 40),
		Components: Components{
			Core:        Component{Repository: "NeKiro-project/NeKiro", CommitSHA: strings.Repeat("a", 40)},
			Console:     Component{Repository: "NeKiro-project/NeKiro-Console", CommitSHA: strings.Repeat("b", 40)},
			SDKGo:       Component{Repository: "NeKiro-project/nekiro-sdk-go", CommitSHA: strings.Repeat("c", 40)},
			Samples:     Component{Repository: "NeKiro-project/NeKiro-Samples", CommitSHA: strings.Repeat("d", 40)},
			TransportGo: Component{Repository: "NeKiro-project/nekiro-a2a-transport-go", CommitSHA: strings.Repeat("e", 40), Tag: "v0.1.1"},
		},
	}
}

func validResolutions(value Manifest) map[string]Resolution {
	resolved := make(map[string]Resolution)
	for _, item := range value.Ordered() {
		imageDigests := make(map[string]string)
		for name, image := range item.Component.Images {
			imageDigests[name] = image.Digest
		}
		resolved[item.Name] = Resolution{CommitSHA: item.Component.CommitSHA, TagCommitSHA: item.Component.CommitSHA, ImageDigests: imageDigests}
	}
	return resolved
}
