package config

import (
	"os"
	"strings"
	"testing"
)

func TestExpandEnv_Required_Missing(t *testing.T) {
	os.Unsetenv("STAFFOPS_TEST_MISSING")
	_, err := expandEnv("url: ${STAFFOPS_TEST_MISSING}")
	if err == nil {
		t.Fatal("expected error for missing required var")
	}
	if !strings.Contains(err.Error(), "STAFFOPS_TEST_MISSING") {
		t.Errorf("error should name the var, got: %v", err)
	}
}

func TestExpandEnv_Required_Set(t *testing.T) {
	t.Setenv("STAFFOPS_TEST_VAR", "hello")
	got, err := expandEnv("greeting: ${STAFFOPS_TEST_VAR}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "greeting: hello" {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnv_Default_Used(t *testing.T) {
	os.Unsetenv("STAFFOPS_TEST_NOPE")
	got, err := expandEnv("v: ${STAFFOPS_TEST_NOPE:fallback}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v: fallback" {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnv_Default_EmptyAllowed(t *testing.T) {
	os.Unsetenv("STAFFOPS_TEST_EMPTY")
	got, err := expandEnv("v: ${STAFFOPS_TEST_EMPTY:}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v: " {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnv_SetOverridesDefault(t *testing.T) {
	t.Setenv("STAFFOPS_TEST_OVERRIDE", "real-value")
	got, err := expandEnv("v: ${STAFFOPS_TEST_OVERRIDE:default}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "v: real-value" {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnv_MultipleVars(t *testing.T) {
	t.Setenv("STAFFOPS_TEST_A", "alpha")
	t.Setenv("STAFFOPS_TEST_B", "beta")
	got, err := expandEnv("a=${STAFFOPS_TEST_A} b=${STAFFOPS_TEST_B} c=${STAFFOPS_TEST_C:gamma}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "a=alpha b=beta c=gamma" {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnv_NoSubstitutions(t *testing.T) {
	got, err := expandEnv("plain: text\nno: variables")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "plain: text\nno: variables" {
		t.Errorf("got %q", got)
	}
}

func TestExpandEnv_SkipsCommentLines(t *testing.T) {
	os.Unsetenv("STAFFOPS_TEST_ONLY_IN_COMMENT")
	// Even though the comment mentions a missing var, no error should be raised.
	got, err := expandEnv("# example syntax: ${STAFFOPS_TEST_ONLY_IN_COMMENT}\nkey: value")
	if err != nil {
		t.Fatalf("comment placeholder should not cause error: %v", err)
	}
	if !strings.Contains(got, "${STAFFOPS_TEST_ONLY_IN_COMMENT}") {
		t.Errorf("comment should be left verbatim, got %q", got)
	}
}

func TestExpandEnv_DefaultWithColons(t *testing.T) {
	os.Unsetenv("STAFFOPS_TEST_ADDR")
	got, err := expandEnv("addr: ${STAFFOPS_TEST_ADDR:redis:6379}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "addr: redis:6379" {
		t.Errorf("got %q", got)
	}
}
