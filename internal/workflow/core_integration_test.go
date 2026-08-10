package workflow

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	stackRevisionExpression = "${{ inputs.stack_sha }}"
	secureProxyImage        = "NEKIRO_NACOS_SECURE_PROXY_IMAGE"
)

type coreIntegrationWorkflow struct {
	On struct {
		WorkflowCall workflowTrigger `yaml:"workflow_call"`
		Dispatch     workflowTrigger `yaml:"workflow_dispatch"`
	} `yaml:"on"`
	Jobs map[string]workflowJob `yaml:"jobs"`
}

type workflowTrigger struct {
	Inputs map[string]workflowInput `yaml:"inputs"`
}

type workflowInput struct {
	Required bool `yaml:"required"`
}

type workflowJob struct {
	Steps []workflowStep `yaml:"steps"`
}

type workflowStep struct {
	Name string            `yaml:"name"`
	With map[string]string `yaml:"with"`
	Run  string            `yaml:"run"`
}

func TestCoreIntegrationPinsStackSourceAndExportsPreparedImages(t *testing.T) {
	content, err := os.ReadFile("../../.github/workflows/core-integration.yml")
	if err != nil {
		t.Fatal(err)
	}
	var workflow coreIntegrationWorkflow
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		t.Fatal(err)
	}
	for triggerName, trigger := range map[string]workflowTrigger{
		"workflow_call":     workflow.On.WorkflowCall,
		"workflow_dispatch": workflow.On.Dispatch,
	} {
		if !trigger.Inputs["stack_sha"].Required {
			t.Errorf("%s stack_sha input must be required", triggerName)
		}
	}
	for _, jobName := range []string{"backend", "browser"} {
		job, ok := workflow.Jobs[jobName]
		if !ok {
			t.Fatalf("missing %s job", jobName)
		}
		checkout := namedStep(t, job.Steps, "Check out canonical Stack")
		if checkout.With["ref"] != stackRevisionExpression {
			t.Errorf("%s checkout ref = %q, want exact stack_sha input", jobName, checkout.With["ref"])
		}
		prepare := namedStep(t, job.Steps, "Resolve exact components and build images")
		if !strings.Contains(prepare.Run, secureProxyImage) {
			t.Errorf("%s prepare step does not export %s", jobName, secureProxyImage)
		}
	}
}

func namedStep(t *testing.T, steps []workflowStep, name string) workflowStep {
	t.Helper()
	for _, step := range steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("missing workflow step %q", name)
	return workflowStep{}
}
