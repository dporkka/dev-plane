package runtimes

import "testing"

func TestNewProviderLocal(t *testing.T) {
	p, name, err := NewProvider("local", t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("NewProvider(local) error: %v", err)
	}
	if name != "local" {
		t.Fatalf("name = %q, want local", name)
	}
	if _, ok := p.(*LocalProvider); !ok {
		t.Fatalf("provider type = %T, want *LocalProvider", p)
	}
}

func TestNewProviderDocker(t *testing.T) {
	p, name, err := NewProvider("docker", t.TempDir(), "", "")
	if err != nil {
		t.Fatalf("NewProvider(docker) error: %v", err)
	}
	if name != "docker" {
		t.Fatalf("name = %q, want docker", name)
	}
	if _, ok := p.(*DockerProvider); !ok {
		t.Fatalf("provider type = %T, want *DockerProvider", p)
	}
}

func TestNewProviderRemote(t *testing.T) {
	p, name, err := NewProvider("remote", "", "http://localhost:8082", "token")
	if err != nil {
		t.Fatalf("NewProvider(remote) error: %v", err)
	}
	if name != "remote" {
		t.Fatalf("name = %q, want remote", name)
	}
	if _, ok := p.(*RemoteProvider); !ok {
		t.Fatalf("provider type = %T, want *RemoteProvider", p)
	}
}

func TestNewProviderRemoteRequiresURL(t *testing.T) {
	_, _, err := NewProvider("remote", "", "", "")
	if err == nil {
		t.Fatal("NewProvider(remote) error = nil, want error")
	}
}

func TestNewProviderUnsupported(t *testing.T) {
	_, _, err := NewProvider("unknown", "", "", "")
	if err == nil {
		t.Fatal("NewProvider(unknown) error = nil, want error")
	}
}
