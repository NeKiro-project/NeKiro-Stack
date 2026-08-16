package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteEvaluationEnvironmentGeneratesFreshSecretsAndPinsEveryImage(t *testing.T) {
	root := t.TempDir()
	image := "ghcr.io/nekiro-project/component:v0.1.0@sha256:" + strings.Repeat("a", 64)
	getenv := func(string) string { return image }
	first := filepath.Join(root, "first.env")
	second := filepath.Join(root, "second.env")
	if err := writeEvaluationEnvironment(root, first, getenv); err != nil {
		t.Fatal(err)
	}
	if err := writeEvaluationEnvironment(root, second, getenv); err != nil {
		t.Fatal(err)
	}
	firstBody, _ := os.ReadFile(first)
	secondBody, _ := os.ReadFile(second)
	if string(firstBody) == string(secondBody) {
		t.Fatal("two evaluation runs reused generated credentials")
	}
	if strings.Count(string(firstBody), image) != 6 {
		t.Fatalf("immutable image count=%d", strings.Count(string(firstBody), image))
	}
	if strings.Contains(string(firstBody), "acceptance-owner-token") {
		t.Fatal("legacy fixed evaluation token was written")
	}
}

func TestWriteEvaluationEnvironmentRejectsMutableImagesAndBroadOutput(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "evaluation.env")
	if err := writeEvaluationEnvironment(root, output, func(string) string { return "ghcr.io/nekiro-project/component:latest" }); err == nil {
		t.Fatal("mutable image was accepted")
	}
	if err := writeEvaluationEnvironment(root, filepath.Join(filepath.Dir(root), "outside.env"), func(string) string { return "" }); err == nil {
		t.Fatal("output outside evaluation root was accepted")
	}
}
