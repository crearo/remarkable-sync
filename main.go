package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	remarkableBaseURL = "http://10.11.99.1"
	manifestFile      = ".sync_manifest.json"
	maxRetries        = 3
	retryDelay        = 1 * time.Second
)

// Color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
)

type Document struct {
	ID             string `json:"ID"`
	VissibleName   string `json:"VissibleName"`
	ModifiedClient string `json:"ModifiedClient"`
	Type           string `json:"Type"`
	FileType       string `json:"fileType"`
	Bookmarked     bool   `json:"Bookmarked"`
	CurrentPage    int    `json:"CurrentPage"`
	Parent         string `json:"Parent"`
}

type ManifestEntry struct {
	LastSync string `json:"last_sync"`
}

type SyncStats struct {
	Downloaded int
	Skipped    int
	Failed     int
	Partial    int
}

func main() {
	fmt.Printf("%s%sreMarkable Sync Utility%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n\n", colorDim, colorReset)

	// Ensure directories exist
	if err := ensureDirectories(); err != nil {
		fmt.Printf("%s✗ Failed to create directories: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	// Load existing manifest
	manifest, err := loadManifest()
	if err != nil {
		fmt.Printf("%s✗ Failed to load manifest: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	// Fetch documents list
	fmt.Printf("%s📡 Fetching documents list...%s\n", colorBlue, colorReset)
	documents, err := fetchDocuments()
	if err != nil {
		fmt.Printf("%s✗ Failed to fetch documents: %v%s\n", colorRed, err, colorReset)
		fmt.Printf("%s💡 Make sure your reMarkable tablet is connected and accessible at %s%s\n", colorYellow, remarkableBaseURL, colorReset)
		os.Exit(1)
	}

	fmt.Printf("%s✓%s Found %d documents\n\n", colorGreen, colorReset, len(documents))

	stats := SyncStats{}

	// Process each document
	for _, doc := range documents {
		fmt.Printf("%s%s%s\n", colorBold, doc.VissibleName, colorReset)
		fmt.Printf("  ID: %s%s%s\n", colorDim, doc.ID, colorReset)

		// Check if we need to download
		if !needsDownload(manifest, doc.ID, doc.ModifiedClient) {
			fmt.Printf("  %s⊘ Skipped (up to date)%s\n\n", colorYellow, colorReset)
			stats.Skipped++
			continue
		}

		// Download PDF
		pdfSuccess := downloadFile(doc.ID, doc.VissibleName, "pdf")

		// Download RMDOC
		rmdocSuccess := downloadFile(doc.ID, doc.VissibleName, "rmdoc")

		// Update stats and manifest
		if pdfSuccess && rmdocSuccess {
			stats.Downloaded++
			manifest[doc.ID] = ManifestEntry{LastSync: doc.ModifiedClient}
		} else if pdfSuccess || rmdocSuccess {
			stats.Partial++
			manifest[doc.ID] = ManifestEntry{LastSync: doc.ModifiedClient}
		} else {
			stats.Failed++
		}

		fmt.Println()
	}

	// Save manifest
	if err := saveManifest(manifest); err != nil {
		fmt.Printf("%s✗ Failed to save manifest: %v%s\n", colorRed, err, colorReset)
		os.Exit(1)
	}

	// Print summary
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
	fmt.Printf("%s%sSummary%s\n", colorBold, colorCyan, colorReset)
	fmt.Printf("  %s✓ Downloaded:%s     %d documents (both formats)\n", colorGreen, colorReset, stats.Downloaded)
	if stats.Partial > 0 {
		fmt.Printf("  %s⚠ Partial:%s        %d documents (one format failed)\n", colorYellow, colorReset, stats.Partial)
	}
	fmt.Printf("  %s⊘ Skipped:%s        %d documents (up to date)\n", colorYellow, colorReset, stats.Skipped)
	if stats.Failed > 0 {
		fmt.Printf("  %s✗ Failed:%s         %d documents (both formats failed)\n", colorRed, colorReset, stats.Failed)
	}
	fmt.Printf("%s━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━%s\n", colorDim, colorReset)
}

func ensureDirectories() error {
	if err := os.MkdirAll("downloads/pdf", 0755); err != nil {
		return err
	}
	if err := os.MkdirAll("downloads/rmdoc", 0755); err != nil {
		return err
	}
	return nil
}

func loadManifest() (map[string]ManifestEntry, error) {
	manifest := make(map[string]ManifestEntry)

	data, err := os.ReadFile(manifestFile)
	if err != nil {
		if os.IsNotExist(err) {
			return manifest, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}

	return manifest, nil
}

func saveManifest(manifest map[string]ManifestEntry) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(manifestFile, data, 0644)
}

func needsDownload(manifest map[string]ManifestEntry, id, modifiedClient string) bool {
	entry, exists := manifest[id]
	if !exists {
		return true
	}

	// Simple string comparison works for ISO 8601 timestamps
	return modifiedClient > entry.LastSync
}

func fetchDocuments() ([]Document, error) {
	resp, err := http.Get(remarkableBaseURL + "/documents/")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status: %d", resp.StatusCode)
	}

	var documents []Document
	if err := json.NewDecoder(resp.Body).Decode(&documents); err != nil {
		return nil, err
	}

	return documents, nil
}

func downloadFile(id, name, format string) bool {
	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			fmt.Printf("  %s↻ Retry %d/%d...%s\n", colorYellow, retry, maxRetries-1, colorReset)
			time.Sleep(retryDelay)

			// Check if we can still reach the tablet before retrying
			if !checkConnectivity() {
				fmt.Printf("  %s✗ Cannot reach reMarkable tablet at %s%s\n", colorRed, remarkableBaseURL, colorReset)
				fmt.Printf("  %s💡 Please check your connection and try again%s\n", colorYellow, colorReset)
				os.Exit(1)
			}
		}

		if downloadFileOnce(id, name, format) {
			fmt.Printf("  %s✓ Downloaded %s%s\n", colorGreen, format, colorReset)
			return true
		}

		if retry == maxRetries-1 {
			fmt.Printf("  %s✗ Failed to download %s%s\n", colorRed, format, colorReset)
			return false
		}
	}

	return false
}

func downloadFileOnce(id, name, format string) bool {
	url := fmt.Sprintf("%s/download/%s/%s", remarkableBaseURL, id, format)

	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false
	}

	// Sanitize filename
	safeName := sanitizeFilename(name)
	filename := filepath.Join("downloads", format, fmt.Sprintf("%s-%s.%s", safeName, id, format))

	file, err := os.Create(filename)
	if err != nil {
		return false
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	return err == nil
}

func checkConnectivity() bool {
	client := &http.Client{
		Timeout: 3 * time.Second,
	}

	resp, err := client.Get(remarkableBaseURL + "/documents/")
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

func sanitizeFilename(name string) string {
	var result strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == ' ' || c == '.' {
			result.WriteRune(c)
		} else {
			result.WriteRune('_')
		}
	}
	return result.String()
}
