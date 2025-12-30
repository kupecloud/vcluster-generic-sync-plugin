//go:build e2e

package e2e

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"
)

func startVClusterPortForward(ctx context.Context, kubeconfigPath, namespace, podName string) (int, func(), error) {
	port, err := findFreePort()
	if err != nil {
		return 0, nil, err
	}

	args := []string{
		"kubectl",
		"--kubeconfig", kubeconfigPath,
		"-n", namespace,
		"port-forward",
		"pod/" + podName,
		fmt.Sprintf("%d:8443", port),
		"--address", "127.0.0.1",
	}

	cmd := exec.Command(args[0], args[1:]...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return 0, nil, err
	}

	if err := cmd.Start(); err != nil {
		return 0, nil, err
	}

	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}

	go func() {
		<-ctx.Done()
		stop()
	}()

	readyCh := make(chan struct{}, 1)
	errCh := make(chan error, 1)

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Forwarding from") {
				readyCh <- struct{}{}
				return
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(line, "Forwarding from") {
				readyCh <- struct{}{}
				return
			}
		}
	}()

	go func() {
		errCh <- cmd.Wait()
	}()

	select {
	case <-readyCh:
		return port, stop, nil
	case err := <-errCh:
		return 0, nil, fmt.Errorf("port-forward failed: %w", err)
	case <-time.After(20 * time.Second):
		stop()
		return 0, nil, fmt.Errorf("timeout waiting for port-forward")
	}
}

func findFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
