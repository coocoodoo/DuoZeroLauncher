package main

import (
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// (the "app" for TestWorkRunsWhileAnAppRuns: this test binary, sleeping)
func TestZZSleepHelper(t *testing.T) {
	if os.Getenv("DUO_TEST_SLEEP") == "1" {
		time.Sleep(1500 * time.Millisecond)
	}
}

// While an app runs, what background work hands to the UI thread still gets done: more of it
// than the queue holds (a stereo reconnecting, a remote's buttons) used to block its senders
// until the app closed.
func TestWorkRunsWhileAnAppRuns(t *testing.T) {
	a := testApp(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestZZSleepHelper$")
	cmd.Env = append(os.Environ(), "DUO_TEST_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Skip("can't start a helper process:", err)
	}
	a.appRunning.Store(true)
	var ran atomic.Int32
	sent := make(chan struct{})
	go func() {
		for i := 0; i < 3*cap(a.work); i++ {
			a.work <- func() { ran.Add(1); a.notify("hello", false) }
		}
		close(sent)
	}()
	if err := a.waitApp(cmd); err != nil {
		t.Fatal(err)
	}
	select {
	case <-sent:
	default:
		t.Fatal("a sender was still waiting when the app closed")
	}
	if int(ran.Load()) != 3*cap(a.work) {
		t.Fatalf("%d of %d pieces of work ran", ran.Load(), 3*cap(a.work))
	}
	if a.toast != "" {
		t.Error("a toast was set while an app had the screen")
	}
}
