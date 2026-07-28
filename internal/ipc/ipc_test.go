package ipc

import (
	"context"
	"net/http"
	"testing"
)

func TestServerClientRoundTripOpenStatusStop(t *testing.T) {
	t.Parallel()
	projectPath := t.TempDir()

	service := &fakeService{
		status: StatusResponse{ThreadID: "thr-1", ProjectPath: projectPath, Running: true},
		open:   OpenResponse{ThreadID: "thr-1", Endpoint: "ws://127.0.0.1:4500", Token: "tok"},
	}
	server := NewServer(service, "local-token")
	addr, err := server.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	client := NewClient(addr, "local-token")
	openResp, err := client.Open(context.Background(), OpenRequest{ProjectPath: projectPath, NewSession: true})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if openResp.ThreadID != "thr-1" {
		t.Fatalf("open thread = %q, want %q", openResp.ThreadID, "thr-1")
	}

	statusResp, err := client.Status(context.Background())
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if statusResp.ProjectPath != projectPath {
		t.Fatalf("status path = %q, want %q", statusResp.ProjectPath, projectPath)
	}

	if err := client.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if service.stopCalls != 1 {
		t.Fatalf("Stop() service calls = %d, want 1", service.stopCalls)
	}
}

func TestServerRejectsUnauthorizedAndOrigin(t *testing.T) {
	t.Parallel()

	service := &fakeService{
		open: OpenResponse{ThreadID: "thr-2", Endpoint: "ws://127.0.0.1:4500", Token: "tok"},
	}
	server := NewServer(service, "local-token")
	addr, err := server.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	client := NewClient(addr, "wrong-token")
	if _, err := client.Open(context.Background(), OpenRequest{ProjectPath: "D:\\repo"}); err == nil {
		t.Fatal("expected unauthorized error")
	}

	// origin header from a local client must be rejected.
	req, err := http.NewRequest(http.MethodGet, addr+statusPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer local-token")
	req.Header.Set("Origin", "http://127.0.0.1")
	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("origin request error = %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status code = %d, want %d", resp.StatusCode, http.StatusForbidden)
	}
}

func TestServerRejectsInvalidProjectPath(t *testing.T) {
	t.Parallel()

	service := &fakeService{
		open: OpenResponse{ThreadID: "thr-2", Endpoint: "ws://127.0.0.1:4500", Token: "tok"},
	}
	server := NewServer(service, "local-token")
	addr, err := server.Start(context.Background())
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	client := NewClient(addr, "local-token")
	if _, err := client.Open(context.Background(), OpenRequest{ProjectPath: "/tmp/../repo", NewSession: true}); err == nil {
		t.Fatal("expected relative path rejection")
	}
	if service.openCalls != 0 {
		t.Fatalf("open calls = %d, want 0", service.openCalls)
	}
}

func TestServerRequiresLoopbackPeer(t *testing.T) {
	// This test asserts loopback detection directly, avoiding network stack dependence.
	t.Parallel()
	if isLoopbackAddress("127.0.0.1:1234") == false {
		t.Fatal("127.0.0.1 should be treated as loopback")
	}
	if isLoopbackAddress("192.0.2.1:1234") {
		t.Fatal("non-loopback address reported as loopback")
	}
}

type fakeService struct {
	open      OpenResponse
	status    StatusResponse
	openErr   error
	stopCalls int
	openCalls int
}

func (f *fakeService) Open(context.Context, OpenRequest) (OpenResponse, error) {
	f.openCalls++
	return f.open, f.openErr
}

func (f *fakeService) Status(context.Context) (StatusResponse, error) {
	return f.status, nil
}

func (f *fakeService) Stop(context.Context) error {
	f.stopCalls++
	return nil
}
