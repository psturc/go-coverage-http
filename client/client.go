package coverageclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// CoverageClient handles coverage collection from Kubernetes pods
type CoverageClient struct {
	clientset       kubernetes.Interface
	restConfig      *rest.Config
	namespace       string
	outputDir       string
	httpClient      *http.Client
	defaultFilters  []string // Default file patterns to filter out from coverage
	sourceDir       string   // Local source directory for path remapping
	enablePathRemap bool     // Whether to automatically remap container paths
}

// CoverageResponse matches the server's response format
type CoverageResponse struct {
	MetaFilename     string `json:"meta_filename"`
	MetaData         string `json:"meta_data"`
	CountersFilename string `json:"counters_filename"`
	CountersData     string `json:"counters_data"`
	TestName         string `json:"test_name"`
	Timestamp        int64  `json:"timestamp"`
}

// NewClient creates a new coverage client for the given namespace
func NewClient(namespace, outputDir string) (*CoverageClient, error) {
	// Load kubeconfig
	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Build config from kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		// Try in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("build kubernetes config: %w", err)
		}
	}

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("create kubernetes client: %w", err)
	}

	// Create output directory if it doesn't exist
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("create output directory: %w", err)
	}

	// Get current working directory as default source directory
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	return &CoverageClient{
		clientset:       clientset,
		restConfig:      config,
		namespace:       namespace,
		outputDir:       outputDir,
		httpClient:      &http.Client{Timeout: 30 * time.Second},
		defaultFilters:  []string{"coverage_server.go"}, // Default: filter out the coverage server itself
		sourceDir:       cwd,
		enablePathRemap: true, // Default: enable automatic path remapping
	}, nil
}

// SetDefaultFilters configures which files to automatically filter from coverage reports
func (c *CoverageClient) SetDefaultFilters(patterns []string) {
	c.defaultFilters = patterns
}

// AddDefaultFilter adds a file pattern to the default filter list
func (c *CoverageClient) AddDefaultFilter(pattern string) {
	c.defaultFilters = append(c.defaultFilters, pattern)
}

// SetSourceDirectory sets the local source directory for path remapping
func (c *CoverageClient) SetSourceDirectory(dir string) {
	c.sourceDir = dir
}

// SetPathRemapping enables or disables automatic path remapping
func (c *CoverageClient) SetPathRemapping(enabled bool) {
	c.enablePathRemap = enabled
}

// GetPodName discovers a pod name dynamically based on label selector
// Example: client.GetPodName("app=coverage-demo")
func (c *CoverageClient) GetPodName(labelSelector string) (string, error) {
	return c.GetPodNameWithContext(context.Background(), labelSelector)
}

// GetPodNameWithContext discovers a pod name with context support
func (c *CoverageClient) GetPodNameWithContext(ctx context.Context, labelSelector string) (string, error) {
	fmt.Printf("🔍 Discovering pod with label selector: %s\n", labelSelector)

	// List pods with the label selector
	pods, err := c.clientset.CoreV1().Pods(c.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found with label selector '%s' in namespace '%s'", labelSelector, c.namespace)
	}

	// Find the first running pod
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning {
			fmt.Printf("✅ Found running pod: %s\n", pod.Name)
			return pod.Name, nil
		}
	}

	// If no running pod found, return first pod with its status
	firstPod := pods.Items[0]
	return "", fmt.Errorf("no running pod found (first pod '%s' is in phase '%s')", firstPod.Name, firstPod.Status.Phase)
}

// CollectCoverageFromPod collects coverage data from a pod via port-forwarding
func (c *CoverageClient) CollectCoverageFromPod(ctx context.Context, podName, testName string, targetPort int) error {
	fmt.Printf("📊 Collecting coverage from pod %s for test: %s\n", podName, testName)

	// Setup port forwarding
	localPort, stopChan, err := c.setupPortForward(podName, targetPort)
	if err != nil {
		return fmt.Errorf("setup port forward: %w", err)
	}
	defer close(stopChan)

	// Wait a bit for port forward to be ready
	time.Sleep(2 * time.Second)

	// Collect coverage via HTTP
	coverageURL := fmt.Sprintf("http://localhost:%d/coverage", localPort)
	if err := c.collectCoverageFromURL(coverageURL, testName); err != nil {
		return fmt.Errorf("collect coverage: %w", err)
	}

	fmt.Printf("✅ Coverage collected successfully for test: %s\n", testName)
	return nil
}

// CollectCoverageFromURL collects coverage data from a direct URL (no port-forwarding)
func (c *CoverageClient) CollectCoverageFromURL(coverageURL, testName string) error {
	return c.collectCoverageFromURL(coverageURL, testName)
}

// setupPortForward sets up port forwarding to the pod
func (c *CoverageClient) setupPortForward(podName string, targetPort int) (int, chan struct{}, error) {
	// Use a local port (let the system choose)
	localPort := 0 // 0 means let the system choose

	path := fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/portforward", c.namespace, podName)
	hostIP := strings.TrimPrefix(c.restConfig.Host, "https://")
	serverURL, err := url.Parse(fmt.Sprintf("https://%s%s", hostIP, path))
	if err != nil {
		return 0, nil, fmt.Errorf("parse server URL: %w", err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(c.restConfig)
	if err != nil {
		return 0, nil, fmt.Errorf("create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", serverURL)

	stopChan := make(chan struct{}, 1)
	readyChan := make(chan struct{})

	// Create port forward
	ports := []string{fmt.Sprintf("%d:%d", localPort, targetPort)}

	out := io.Discard
	errOut := io.Discard

	forwarder, err := portforward.New(dialer, ports, stopChan, readyChan, out, errOut)
	if err != nil {
		return 0, nil, fmt.Errorf("create port forwarder: %w", err)
	}

	// Start port forwarding in background
	go func() {
		if err := forwarder.ForwardPorts(); err != nil {
			fmt.Printf("⚠️  Port forward error: %v\n", err)
		}
	}()

	// Wait for ready signal
	select {
	case <-readyChan:
		// Get the actual local port that was assigned
		forwardedPorts, err := forwarder.GetPorts()
		if err != nil || len(forwardedPorts) == 0 {
			close(stopChan)
			return 0, nil, fmt.Errorf("get forwarded ports: %w", err)
		}
		actualLocalPort := int(forwardedPorts[0].Local)
		fmt.Printf("✅ Port forward ready: localhost:%d -> pod:%d\n", actualLocalPort, targetPort)
		return actualLocalPort, stopChan, nil
	case <-time.After(30 * time.Second):
		close(stopChan)
		return 0, nil, fmt.Errorf("timeout waiting for port forward")
	}
}

// collectCoverageFromURL collects coverage from the given URL
func (c *CoverageClient) collectCoverageFromURL(coverageURL, testName string) error {
	// Prepare request body
	reqBody, err := json.Marshal(map[string]string{
		"test_name": testName,
	})
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	// Send POST request to coverage endpoint
	resp, err := c.httpClient.Post(coverageURL, "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("send coverage request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("coverage endpoint returned %d: %s", resp.StatusCode, body)
	}

	// Parse response
	var covResp CoverageResponse
	if err := json.NewDecoder(resp.Body).Decode(&covResp); err != nil {
		return fmt.Errorf("decode coverage response: %w", err)
	}

	// Decode and save metadata
	metaData, err := base64.StdEncoding.DecodeString(covResp.MetaData)
	if err != nil {
		return fmt.Errorf("decode metadata: %w", err)
	}

	// Decode and save counters
	counterData, err := base64.StdEncoding.DecodeString(covResp.CountersData)
	if err != nil {
		return fmt.Errorf("decode counters: %w", err)
	}

	// Create test-specific subdirectory
	testDir := filepath.Join(c.outputDir, testName)
	if err := os.MkdirAll(testDir, 0755); err != nil {
		return fmt.Errorf("create test directory: %w", err)
	}

	// Save files with proper names
	metaPath := filepath.Join(testDir, covResp.MetaFilename)
	if err := os.WriteFile(metaPath, metaData, 0644); err != nil {
		return fmt.Errorf("write metadata file: %w", err)
	}

	counterPath := filepath.Join(testDir, covResp.CountersFilename)
	if err := os.WriteFile(counterPath, counterData, 0644); err != nil {
		return fmt.Errorf("write counters file: %w", err)
	}

	fmt.Printf("  📁 Saved: %s\n", metaPath)
	fmt.Printf("  📁 Saved: %s\n", counterPath)

	return nil
}

// GenerateCoverageReport generates a text coverage report from collected data
func (c *CoverageClient) GenerateCoverageReport(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage.out")

	fmt.Printf("📊 Generating coverage report for test: %s\n", testName)

	// Run go tool covdata to convert binary format to text
	cmd := exec.Command("go", "tool", "covdata", "textfmt",
		"-i="+testDir,
		"-o="+reportPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate coverage report: %w\nOutput: %s", err, output)
	}

	fmt.Printf("✅ Coverage report generated: %s\n", reportPath)

	// Apply path remapping if enabled
	if c.enablePathRemap {
		if err := c.remapCoveragePaths(reportPath); err != nil {
			fmt.Printf("⚠️  Path remapping failed: %v (continuing with original paths)\n", err)
		}
	}

	return nil
}

// FilterCoverageReport filters out specified files from the coverage report.
// If no patterns are provided, uses the client's default filters.
// Pass an empty slice []string{} to disable all filtering.
func (c *CoverageClient) FilterCoverageReport(testName string, patterns ...string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage.out")
	filteredPath := filepath.Join(testDir, "coverage_filtered.out")

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read coverage report: %w", err)
	}

	// Use default filters if no patterns provided
	filterPatterns := patterns
	if len(patterns) == 0 {
		filterPatterns = c.defaultFilters
	}

	// If no filters at all, just copy the file
	if len(filterPatterns) == 0 {
		if err := os.WriteFile(filteredPath, data, 0644); err != nil {
			return fmt.Errorf("write filtered report: %w", err)
		}
		fmt.Printf("✅ Coverage report (no filters applied): %s\n", filteredPath)
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var filtered []string
	filteredCount := 0

	for _, line := range lines {
		shouldFilter := false
		for _, pattern := range filterPatterns {
			if pattern != "" && strings.Contains(line, pattern) {
				shouldFilter = true
				filteredCount++
				break
			}
		}
		if !shouldFilter {
			filtered = append(filtered, line)
		}
	}

	filteredData := strings.Join(filtered, "\n")
	if err := os.WriteFile(filteredPath, []byte(filteredData), 0644); err != nil {
		return fmt.Errorf("write filtered report: %w", err)
	}

	fmt.Printf("✅ Filtered coverage report: %s (removed %d lines matching: %v)\n",
		filteredPath, filteredCount, filterPatterns)
	return nil
}

// GenerateHTMLReport generates an HTML coverage report
func (c *CoverageClient) GenerateHTMLReport(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage_filtered.out")
	htmlPath := filepath.Join(testDir, "coverage.html")

	// Check if filtered report exists, fallback to regular report
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		reportPath = filepath.Join(testDir, "coverage.out")
	}

	fmt.Printf("📊 Generating HTML coverage report for test: %s\n", testName)

	cmd := exec.Command("go", "tool", "cover",
		"-html="+reportPath,
		"-o="+htmlPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate HTML report: %w\nOutput: %s", err, output)
	}

	fmt.Printf("✅ HTML report generated: %s\n", htmlPath)
	return nil
}

// PrintCoverageSummary prints a summary of the coverage data
func (c *CoverageClient) PrintCoverageSummary(testName string) error {
	testDir := filepath.Join(c.outputDir, testName)
	reportPath := filepath.Join(testDir, "coverage_filtered.out")

	// Check if filtered report exists, fallback to regular report
	if _, err := os.Stat(reportPath); os.IsNotExist(err) {
		reportPath = filepath.Join(testDir, "coverage.out")
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read coverage report: %w", err)
	}

	fmt.Printf("\n📊 Coverage Summary for test: %s\n", testName)
	fmt.Println(strings.Repeat("=", 60))
	fmt.Println(string(data))
	fmt.Println(strings.Repeat("=", 60))

	return nil
}

// ProcessCoverageReports is a convenience method that generates, filters, and creates HTML reports
// all in one call. It automatically uses the client's default filters.
func (c *CoverageClient) ProcessCoverageReports(testName string) error {
	// Generate text report from binary coverage data
	if err := c.GenerateCoverageReport(testName); err != nil {
		return fmt.Errorf("generate report: %w", err)
	}

	// Filter the report (uses default filters)
	if err := c.FilterCoverageReport(testName); err != nil {
		return fmt.Errorf("filter report: %w", err)
	}

	// Generate HTML report
	if err := c.GenerateHTMLReport(testName); err != nil {
		// HTML generation might fail if source files aren't available, log but don't fail
		fmt.Printf("⚠️  HTML report generation failed (source files may not be available): %v\n", err)
	}

	return nil
}

// remapCoveragePaths remaps container paths in the coverage report to local paths
func (c *CoverageClient) remapCoveragePaths(reportPath string) error {
	// Read the coverage report
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return fmt.Errorf("read coverage report: %w", err)
	}

	lines := strings.Split(string(data), "\n")

	// Detect container path mappings
	pathMappings := c.detectContainerPaths(lines)

	if len(pathMappings) == 0 {
		fmt.Println("📍 No container paths detected, using paths as-is")
		return nil
	}

	fmt.Printf("📍 Auto-detected path mappings:\n")
	for containerPath, localPath := range pathMappings {
		fmt.Printf("   %s -> %s\n", containerPath, localPath)
	}

	// Remap paths in the coverage data
	var remappedLines []string
	remappedCount := 0

	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "mode:") {
			remappedLines = append(remappedLines, line)
			continue
		}

		// Coverage line format: path/to/file.go:line.col,line.col num count
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			remappedLines = append(remappedLines, line)
			continue
		}

		filePath := parts[0]
		rest := parts[1]

		// Try to remap the path
		newPath := filePath
		for containerPrefix, localPrefix := range pathMappings {
			if strings.HasPrefix(filePath, containerPrefix) {
				newPath = strings.Replace(filePath, containerPrefix, localPrefix, 1)
				remappedCount++
				break
			}
		}

		remappedLines = append(remappedLines, newPath+":"+rest)
	}

	// Write the remapped coverage report back
	remappedData := strings.Join(remappedLines, "\n")
	if err := os.WriteFile(reportPath, []byte(remappedData), 0644); err != nil {
		return fmt.Errorf("write remapped report: %w", err)
	}

	fmt.Printf("✅ Path remapping complete (%d lines remapped)\n", remappedCount)
	return nil
}

// detectContainerPaths analyzes coverage report lines to detect container path mappings
func (c *CoverageClient) detectContainerPaths(lines []string) map[string]string {
	// Collect all file paths from the coverage report
	var coverageFiles []string
	for _, line := range lines {
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}

		// Coverage line format: path/to/file.go:line.col,line.col num count
		parts := strings.SplitN(line, ":", 2)
		if len(parts) >= 1 {
			filePath := parts[0]
			// Only add unique paths
			if len(coverageFiles) == 0 || coverageFiles[len(coverageFiles)-1] != filePath {
				coverageFiles = append(coverageFiles, filePath)
			}
		}
	}

	// Find files that don't exist locally (container paths)
	var containerFiles []string
	for _, filePath := range coverageFiles {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			containerFiles = append(containerFiles, filePath)
		}
	}

	if len(containerFiles) == 0 {
		// No container paths detected
		return nil
	}

	fmt.Printf("   Detected %d container paths to remap\n", len(containerFiles))

	// Get absolute path for source directory
	absSourceDir, err := filepath.Abs(c.sourceDir)
	if err != nil {
		fmt.Printf("   Warning: Could not get absolute path for %s: %v\n", c.sourceDir, err)
		absSourceDir = c.sourceDir
	}

	fmt.Printf("   Searching for source files in: %s\n", absSourceDir)

	// Build a map of local Go files by their relative path structure
	localFilesByRelPath := make(map[string]string) // key: relative path parts joined, value: full path

	err = filepath.Walk(absSourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		if info.IsDir() {
			// Skip common directories that won't have source code
			baseName := filepath.Base(path)
			if baseName == ".git" || baseName == "vendor" || baseName == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(path, ".go") {
			// Store the full path indexed by filename and path structure
			relPath, _ := filepath.Rel(absSourceDir, path)
			localFilesByRelPath[relPath] = path
		}
		return nil
	})

	if err != nil {
		fmt.Printf("   Warning: Error walking source directory: %v\n", err)
		return nil
	}

	fmt.Printf("   Found %d Go source files\n", len(localFilesByRelPath))

	// Try to match container files to local files
	type match struct {
		containerFile string
		localFile     string
		matchScore    int
	}

	var matches []match

	for _, containerFile := range containerFiles {
		containerPath := filepath.Clean(containerFile)
		containerParts := strings.Split(containerPath, string(filepath.Separator))
		fileName := filepath.Base(containerPath)

		// Find best matching local file
		bestMatch := ""
		bestScore := 0

		for relPath, localPath := range localFilesByRelPath {
			localParts := strings.Split(relPath, string(filepath.Separator))

			// Files must have same name
			if filepath.Base(localPath) != fileName {
				continue
			}

			// Count matching suffix parts (from filename backwards)
			matchScore := 0
			maxLen := len(containerParts)
			if len(localParts) < maxLen {
				maxLen = len(localParts)
			}

			for i := 1; i <= maxLen; i++ {
				cIdx := len(containerParts) - i
				lIdx := len(localParts) - i
				if containerParts[cIdx] == localParts[lIdx] {
					matchScore = i
				} else {
					break
				}
			}

			// Prefer longer matches (more specific paths)
			if matchScore > bestScore {
				bestScore = matchScore
				bestMatch = localPath
			}
		}

		if bestMatch != "" && bestScore > 0 {
			matches = append(matches, match{
				containerFile: containerFile,
				localFile:     bestMatch,
				matchScore:    bestScore,
			})
			fmt.Printf("   Match: %s -> %s (score: %d)\n", containerFile, bestMatch, bestScore)
		}
	}

	if len(matches) == 0 {
		fmt.Printf("   No matching files found between container and local paths\n")
		return nil
	}

	fmt.Printf("   Found %d matches between container and local files\n", len(matches))

	// Determine the most common container root prefix
	containerRootCounts := make(map[string]int)

	for _, m := range matches {
		containerParts := strings.Split(filepath.Clean(m.containerFile), string(filepath.Separator))
		// Extract container root (everything except the matched suffix)
		rootPartsCount := len(containerParts) - m.matchScore
		fmt.Printf("   Container: %s, parts: %v, score: %d, rootPartsCount: %d\n",
			m.containerFile, containerParts, m.matchScore, rootPartsCount)
		if rootPartsCount > 0 {
			rootParts := containerParts[:rootPartsCount]
			containerRoot := string(filepath.Separator) + filepath.Join(rootParts...)
			if !strings.HasSuffix(containerRoot, string(filepath.Separator)) {
				containerRoot += string(filepath.Separator)
			}
			fmt.Printf("   -> Container root candidate: %s\n", containerRoot)
			containerRootCounts[containerRoot]++
		}
	}

	// Find the most common container root
	var bestContainerRoot string
	maxCount := 0
	for root, count := range containerRootCounts {
		if count > maxCount {
			maxCount = count
			bestContainerRoot = root
		}
	}

	if bestContainerRoot == "" {
		fmt.Printf("   Could not determine container root\n")
		return nil
	}

	fmt.Printf("   Detected container root: %s\n", bestContainerRoot)

	// Calculate the local root from all matches - find the common ancestor
	// This ensures we get the project root, not a subdirectory
	var localRootCandidates []string
	for _, m := range matches {
		if strings.HasPrefix(m.containerFile, bestContainerRoot) {
			// Get the local root by removing the matching suffix from local path
			localPath := filepath.Clean(m.localFile)
			localParts := strings.Split(localPath, string(filepath.Separator))
			rootPartsCount := len(localParts) - m.matchScore

			if rootPartsCount > 0 {
				rootParts := localParts[:rootPartsCount]
				candidateRoot := string(filepath.Separator) + filepath.Join(rootParts...)
				if !strings.HasSuffix(candidateRoot, string(filepath.Separator)) {
					candidateRoot += string(filepath.Separator)
				}
				localRootCandidates = append(localRootCandidates, candidateRoot)
				fmt.Printf("   Root candidate from %s: %s\n", filepath.Base(m.localFile), candidateRoot)
			}
		}
	}

	// Find the shortest (most common) root - the one closest to the actual source root
	var localRoot string
	if len(localRootCandidates) > 0 {
		localRoot = localRootCandidates[0]
		for _, candidate := range localRootCandidates {
			// Shorter path means closer to the root
			if len(candidate) < len(localRoot) {
				localRoot = candidate
			}
		}
	}

	if localRoot == "" {
		fmt.Printf("   Could not determine local root\n")
		return nil
	}

	fmt.Printf("   Detected local root: %s\n", localRoot)

	// Return the path mapping
	return map[string]string{
		bestContainerRoot: localRoot,
	}
}
