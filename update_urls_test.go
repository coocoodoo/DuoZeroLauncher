package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestUpdateURLs(t *testing.T) {
	sha := "949faa8a82aa56159e6770767798711820079ff8"
	m, tb := updateURLs(sha)
	if !strings.HasSuffix(m, "/"+sha+"/main.go") || !strings.HasSuffix(tb, "/tar.gz/"+sha) {
		t.Errorf("by commit: %s %s", m, tb)
	}
	m, tb = updateURLs("main")
	if !strings.HasSuffix(m, "/main/main.go") || !strings.HasSuffix(tb, "/tar.gz/refs/heads/main") {
		t.Errorf("by branch: %s %s", m, tb)
	}
	if shaRe.MatchString("main") || shaRe.MatchString(sha[:39]) {
		t.Error("shaRe")
	}
}

// DSL_UPDATE_LIVE=1: ask GitHub for real (the API and the raw file at that commit).
func TestLatestVersionLive(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil || os.Getenv("DSL_UPDATE_LIVE") != "1" {
		t.Skip("set DSL_UPDATE_LIVE=1")
	}
	v, err := latestVersion()
	if err != nil || !shaRe.MatchString(updateRef) {
		t.Fatalf("latest %q via %q: %v", v, updateRef, err)
	}
	t.Logf("GitHub has %s at %s", v, updateRef)
}
