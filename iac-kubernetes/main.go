package main

import (
	"bufio"
	"bytes"
	"context"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
)

var allClusters = []string{"paymentsCluster", "streamingCluster", "sreCluster"}
var singleCluster = []string{"sreCluster"}

var kubeMu sync.Mutex

func checkBinary(name string) {
	if _, err := exec.LookPath(name); err != nil {
		log.Fatalf("%s is not installed", name)
	}
}

func run(ctx context.Context, cmd string, args ...string) error {
	c := exec.CommandContext(ctx, cmd, args...)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

func output(cmd string, args ...string) string {
	c := exec.Command(cmd, args...)
	var out bytes.Buffer
	c.Stdout = &out
	c.Stderr = os.Stderr
	if err := c.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func kubectl(ctx context.Context, kubeCtx string, args ...string) error {
	kubeMu.Lock()
	defer kubeMu.Unlock()
	return run(ctx, "kubectl", append([]string{"--context", kubeCtx}, args...)...)
}

func kubectlOut(ctx context.Context, kubeCtx string, args ...string) string {
	kubeMu.Lock()
	defer kubeMu.Unlock()
	return output("kubectl", append([]string{"--context", kubeCtx}, args...)...)
}

func waitFor(ctx context.Context, interval, timeout time.Duration, fn func() bool) bool {
	t := time.NewTicker(interval)
	defer t.Stop()
	timer := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return false
		case <-timer:
			return false
		case <-t.C:
			if fn() {
				return true
			}
		}
	}
}

func repoRoot() string {
	_, srcFile, _, ok := runtime.Caller(0)
	if !ok {
		log.Fatal("cannot resolve source path")
	}
	return filepath.Dir(filepath.Dir(srcFile))
}

func loadEnv() {
	var envPath string
	_, srcFile, _, ok := runtime.Caller(0)
	if ok {
		candidate := filepath.Join(filepath.Dir(srcFile), ".env")
		if _, err := os.Stat(candidate); err == nil {
			envPath = candidate
		}
	}
	if envPath == "" {
		exe, err := os.Executable()
		if err == nil {
			candidate := filepath.Join(filepath.Dir(exe), ".env")
			if _, err := os.Stat(candidate); err == nil {
				envPath = candidate
			}
		}
	}
	if envPath == "" {
		log.Fatal(".env file not found")
	}
	f, err := os.Open(envPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) == 2 {
			os.Setenv(kv[0], kv[1])
		}
	}
}

func startClustersSequential(ctx context.Context, clusters []string) error {
	for _, c := range clusters {
		if err := run(ctx, "minikube", "start", "-p", c, "--driver=docker"); err != nil {
			return err
		}
	}
	return nil
}

func installPrometheusCRDs(ctx context.Context, kubeCtx string) error {
	crdURL := "https://github.com/prometheus-operator/prometheus-operator/releases/download/v0.76.2/stripped-down-crds.yaml"
	return kubectl(ctx, kubeCtx, "apply", "-f", crdURL)
}

func argocdHasInsecure(ctx context.Context, kubeCtx string) bool {
	out := kubectlOut(ctx, kubeCtx,
		"-n", "argocd",
		"get", "deployment", "argocd-server",
		"-o", "jsonpath={.spec.template.spec.containers[0].args}",
	)
	return strings.Contains(out, "--insecure")
}

func patchArgoCD(ctx context.Context, kubeCtx string) {
	kubectl(ctx, kubeCtx,
		"patch", "configmap", "argocd-cm",
		"-n", "argocd",
		"--type", "merge",
		"-p", `{"data":{"server.insecure":"true"}}`,
	)
	if !argocdHasInsecure(ctx, kubeCtx) {
		kubectl(ctx, kubeCtx,
			"patch", "deployment", "argocd-server",
			"-n", "argocd",
			"--type", "json",
			"-p", `[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--insecure"}]`,
		)
	}
	kubectl(ctx, kubeCtx,
		"rollout", "restart", "deployment/argocd-server",
		"-n", "argocd",
	)
}

func installArgoCD(ctx context.Context, kubeCtx string) {
	run(ctx, "bash", "-c",
		"kubectl --context "+kubeCtx+" create ns argocd --dry-run=client -o yaml | kubectl --context "+kubeCtx+" apply -f -")
	kubectl(ctx, kubeCtx, "apply", "-n", "argocd",
		"-f", "https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml")
	waitFor(ctx, 2*time.Second, 3*time.Minute, func() bool {
		return kubectl(ctx, kubeCtx, "-n", "argocd", "get", "deployment", "argocd-server") == nil
	})
	patchArgoCD(ctx, kubeCtx)
}

func applyRootApp(ctx context.Context, kubeCtx string) {
	root := filepath.Join(repoRoot(), "argocd", "root.yaml")
	if err := kubectl(ctx, kubeCtx, "apply", "-n", "argocd", "-f", root); err != nil {
		log.Fatal(err)
	}
}

func waitForArgoCDServerReady(ctx context.Context, kubeCtx string) error {
	ok := waitFor(ctx, 2*time.Second, 5*time.Minute, func() bool {
		out := kubectlOut(ctx, kubeCtx,
			"-n", "argocd",
			"get", "pod",
			"-l", "app.kubernetes.io/name=argocd-server",
			"-o", "jsonpath={.items[0].status.containerStatuses[0].ready}",
		)
		return out == "true"
	})
	if !ok {
		return context.DeadlineExceeded
	}
	return nil
}

func portForwardArgoCD(ctx context.Context, kubeCtx string, localPort string) error {
	cmd := exec.CommandContext(
		ctx,
		"kubectl", "--context", kubeCtx,
		"-n", "argocd",
		"port-forward",
		"svc/argocd-server",
		localPort+":443",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func startNgrok(ctx context.Context, port string) error {
	token := os.Getenv("NGROK_AUTHTOKEN")
	if token == "" {
		return nil
	}
	_ = exec.Command("ngrok", "config", "add-authtoken", token).Run()
	cmd := exec.CommandContext(ctx, "ngrok", "tcp", port)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func waitForArgoCDApps(ctx context.Context, kubeCtx string) {
	ok := waitFor(ctx, 5*time.Second, 10*time.Minute, func() bool {
		out := kubectlOut(ctx, kubeCtx,
			"-n", "argocd",
			"get", "applications",
			"-o", "jsonpath={range .items[*]}{.status.health.status}{\" \"}{end}",
		)
		if out == "" {
			return false
		}
		for _, s := range strings.Fields(out) {
			if s != "Healthy" {
				return false
			}
		}
		return true
	})
	if !ok {
		log.Fatal("argocd applications not healthy")
	}
}

func waitForLocalPort(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if time.Now().After(deadline) {
				return context.DeadlineExceeded
			}
			conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err == nil {
				conn.Close()
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func main() {
	log.SetFlags(0)

	checkBinary("minikube")
	checkBinary("kubectl")
	checkBinary("ngrok")

	loadEnv()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()

	activeClusters := allClusters
	if len(os.Args) > 1 && os.Args[1] == "--single" {
		activeClusters = singleCluster
	}

	if err := startClustersSequential(ctx, activeClusters); err != nil {
		log.Fatal(err)
	}

	for _, c := range activeClusters {
		if err := installPrometheusCRDs(ctx, c); err != nil {
			log.Fatal(err)
		}
	}

	installArgoCD(ctx, "sreCluster")
	applyRootApp(ctx, "sreCluster")

	if err := waitForArgoCDServerReady(ctx, "sreCluster"); err != nil {
		log.Fatal("argocd-server never became ready")
	}

	go func() {
		if err := portForwardArgoCD(ctx, "sreCluster", "8088"); err != nil {
			log.Println("argocd port-forward exited:", err)
		}
	}()

	if err := waitForLocalPort(ctx, "127.0.0.1:8088", 30*time.Second); err != nil {
		log.Fatal("argocd port-forward never became ready")
	}

	// go func() {
	// 	if err := startNgrok(ctx, "8088"); err != nil {
	// 		log.Println("ngrok exited:", err)
	// 	}
	// }()

	waitForArgoCDApps(ctx, "sreCluster")

	<-ctx.Done()
}
