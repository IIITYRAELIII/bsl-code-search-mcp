package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

type Backend struct {
	command *exec.Cmd
	client  *BackendClient
	done    chan struct{}
	waitErr error
	release func()
}

func StartBackend(ctx context.Context, indexDir, zoektBin string, logs io.Writer) (*Backend, error) {
	executable, err := resolveZoektExecutable(zoektBin, "zoekt-webserver")
	if err != nil {
		return nil, err
	}
	port, err := reserveLocalPort()
	if err != nil {
		return nil, err
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	command := newBackendCommand(
		executable,
		"-index", indexDir,
		"-listen", fmt.Sprintf("127.0.0.1:%d", port),
		"-html=false",
		"-rpc=true",
	)
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start Zoekt backend: %w", err)
	}
	release, err := bindBackendProcess(command)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return nil, fmt.Errorf("bind Zoekt backend lifetime: %w", err)
	}
	backend := &Backend{
		command: command,
		done:    make(chan struct{}),
		release: release,
		client: &BackendClient{
			baseURL: baseURL,
			http:    &http.Client{Timeout: 30 * time.Second},
		},
	}
	go func() {
		backend.waitErr = command.Wait()
		close(backend.done)
	}()
	if err := backend.waitReady(ctx, 30*time.Second); err != nil {
		backend.Close()
		return nil, err
	}
	return backend, nil
}

func (b *Backend) Client() *BackendClient {
	return b.client
}

func (b *Backend) Close() {
	if b == nil || b.command == nil || b.command.Process == nil {
		return
	}
	select {
	case <-b.done:
		if b.release != nil {
			b.release()
			b.release = nil
		}
		return
	default:
		if b.release != nil {
			b.release()
			b.release = nil
		} else {
			_ = b.command.Process.Kill()
		}
		<-b.done
	}
}

func (b *Backend) waitReady(ctx context.Context, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("Zoekt backend did not become ready within 30 seconds")
		case <-b.done:
			return fmt.Errorf("Zoekt backend exited before becoming ready: %v", b.waitErr)
		case <-ticker.C:
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, b.client.baseURL+"/healthz", nil)
			if err != nil {
				return err
			}
			response, err := b.client.http.Do(request)
			if err == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

func resolveZoektExecutable(explicitDir, baseName string) (string, error) {
	directory := explicitDir
	if directory == "" {
		directory = os.Getenv("BSL_CODE_SEARCH_ZOEKT_BIN")
	}
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	fileName := baseName + suffix
	if directory != "" {
		candidate := filepath.Join(directory, fileName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
		return "", fmt.Errorf("%s not found in %s", fileName, directory)
	}
	current, err := os.Executable()
	if err == nil {
		candidate := filepath.Join(filepath.Dir(current), fileName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	if candidate, err := exec.LookPath(baseName); err == nil {
		return candidate, nil
	}
	return "", fmt.Errorf("%s not found; place it next to %s or use --zoekt-bin PATH", fileName, appName)
}

func reserveLocalPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("reserve local backend port: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func newBackendCommand(executable string, args ...string) *exec.Cmd {
	return exec.Command(executable, args...)
}
