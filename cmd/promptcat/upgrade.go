package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	releaseAPIURL   = "https://api.github.com/repos/Vortex93/promptcat/releases/latest"
	maxDownloadSize = 100 << 20
)

type githubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func upgrade() error {
	release, err := fetchLatestRelease()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(release.TagName, "v") {
		return fmt.Errorf("latest release has invalid tag %q", release.TagName)
	}
	comparison, err := compareVersions(version, release.TagName)
	if err != nil {
		return err
	}
	if comparison == 0 {
		fmt.Printf("promptcat %s is already latest.\n", version)
		return nil
	}
	if comparison > 0 {
		fmt.Printf("promptcat %s is newer than latest release %s; refusing downgrade.\n", version, release.TagName)
		return nil
	}

	assetName := releaseAssetName(release.TagName)
	assetURL, checksumsURL, err := releaseURLs(release, assetName)
	if err != nil {
		return err
	}
	archiveData, err := download(assetURL)
	if err != nil {
		return fmt.Errorf("download release: %w", err)
	}
	checksums, err := download(checksumsURL)
	if err != nil {
		return fmt.Errorf("download checksums: %w", err)
	}
	if err := verifyChecksum(archiveData, assetName, checksums); err != nil {
		return err
	}

	binary, err := extractBinary(archiveData, assetName)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find installed executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("resolve installed executable: %w", err)
	}
	if err := installBinary(executable, binary); err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		fmt.Printf("Upgrade to %s scheduled; restart promptcat after this process exits.\n", release.TagName)
	} else {
		fmt.Printf("Upgraded promptcat to %s.\n", release.TagName)
	}
	return nil
}

func parseStableVersion(value string) ([3]uint64, error) {
	value = strings.TrimPrefix(value, "v")
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return [3]uint64{}, fmt.Errorf("invalid version %q", value)
	}

	var result [3]uint64
	for i, part := range parts {
		number, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return [3]uint64{}, fmt.Errorf("invalid version %q", value)
		}
		result[i] = number
	}
	return result, nil
}

func compareVersions(a, b string) (int, error) {
	left, err := parseStableVersion(a)
	if err != nil {
		return 0, err
	}
	right, err := parseStableVersion(b)
	if err != nil {
		return 0, err
	}

	for i := range left {
		if left[i] < right[i] {
			return -1, nil
		}
		if left[i] > right[i] {
			return 1, nil
		}
	}
	return 0, nil
}

func fetchLatestRelease() (githubRelease, error) {
	var release githubRelease
	request, err := http.NewRequest(http.MethodGet, releaseAPIURL, nil)
	if err != nil {
		return release, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "promptcat-upgrade")
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		return release, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release, fmt.Errorf("GitHub API returned %s", response.Status)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return release, fmt.Errorf("decode GitHub release: %w", err)
	}
	return release, nil
}

func releaseAssetName(tag string) string {
	version := strings.TrimPrefix(tag, "v")
	archive := "tar.gz"
	if runtime.GOOS == "windows" {
		archive = "zip"
	}
	return fmt.Sprintf("promptcat_%s_%s_%s.%s", version, runtime.GOOS, runtime.GOARCH, archive)
}

func releaseURLs(release githubRelease, assetName string) (string, string, error) {
	var assetURL, checksumsURL string
	for _, asset := range release.Assets {
		switch asset.Name {
		case assetName:
			assetURL = asset.BrowserDownloadURL
		case "checksums.txt":
			checksumsURL = asset.BrowserDownloadURL
		}
	}
	if assetURL == "" || checksumsURL == "" {
		return "", "", fmt.Errorf("release %s lacks %s or checksums.txt", release.TagName, assetName)
	}
	return assetURL, checksumsURL, nil
}

func download(url string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", "promptcat-upgrade")
	response, err := (&http.Client{Timeout: 2 * time.Minute}).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned %s", response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDownloadSize {
		return nil, fmt.Errorf("download exceeds %d MB limit", maxDownloadSize>>20)
	}
	return data, nil
}

func verifyChecksum(data []byte, assetName string, checksums []byte) error {
	for _, line := range strings.Split(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == assetName {
			digest := sha256.Sum256(data)
			if !strings.EqualFold(fields[0], hex.EncodeToString(digest[:])) {
				return fmt.Errorf("checksum mismatch for %s", assetName)
			}
			return nil
		}
	}
	return fmt.Errorf("checksum for %s not found", assetName)
}

func extractBinary(data []byte, assetName string) ([]byte, error) {
	binaryName := "promptcat"
	if runtime.GOOS == "windows" {
		binaryName = "promptcat.exe"
	}
	if strings.HasSuffix(assetName, ".zip") {
		archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("read release archive: %w", err)
		}
		for _, file := range archive.File {
			if path.Base(file.Name) != binaryName || file.FileInfo().IsDir() || file.Mode()&os.ModeSymlink != 0 {
				continue
			}
			reader, err := file.Open()
			if err != nil {
				return nil, err
			}
			binary, readErr := io.ReadAll(io.LimitReader(reader, maxDownloadSize+1))
			reader.Close()
			if readErr != nil {
				return nil, readErr
			}
			if len(binary) > maxDownloadSize {
				return nil, fmt.Errorf("binary exceeds %d MB limit", maxDownloadSize>>20)
			}
			return binary, nil
		}
	} else {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("read release archive: %w", err)
		}
		archive := tar.NewReader(reader)
		for {
			header, err := archive.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				reader.Close()
				return nil, fmt.Errorf("read release archive: %w", err)
			}
			if path.Base(header.Name) != binaryName || (header.Typeflag != tar.TypeReg && header.Typeflag != 0) {
				continue
			}
			binary, readErr := io.ReadAll(io.LimitReader(archive, maxDownloadSize+1))
			reader.Close()
			if readErr != nil {
				return nil, readErr
			}
			if len(binary) > maxDownloadSize {
				return nil, fmt.Errorf("binary exceeds %d MB limit", maxDownloadSize>>20)
			}
			return binary, nil
		}
		reader.Close()
	}
	return nil, fmt.Errorf("%s not found in release archive", binaryName)
}

func installBinary(executable string, binary []byte) error {
	info, err := os.Stat(executable)
	if err != nil {
		return fmt.Errorf("stat installed executable: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(executable), ".promptcat-upgrade-*")
	if err != nil {
		return fmt.Errorf("create upgrade file: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		if cleanup {
			os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		temporary.Close()
		return fmt.Errorf("set upgrade file permissions: %w", err)
	}
	_, err = temporary.Write(binary)
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write upgrade file: %w", err)
	}

	if runtime.GOOS == "windows" {
		if err := scheduleWindowsInstall(temporaryPath, executable); err != nil {
			return err
		}
		cleanup = false
		return nil
	}
	if err := os.Rename(temporaryPath, executable); err != nil {
		return fmt.Errorf("replace installed executable: %w", err)
	}
	cleanup = false
	return nil
}

func scheduleWindowsInstall(temporaryPath, executable string) error {
	script := fmt.Sprintf("$source='%s'; $target='%s'; for ($i=0; $i -lt 20; $i++) { try { Move-Item -LiteralPath $source -Destination $target -Force; Remove-Item -LiteralPath $PSCommandPath -Force -ErrorAction SilentlyContinue; exit 0 } catch { Start-Sleep -Milliseconds 250 } }; exit 1", strings.ReplaceAll(temporaryPath, "'", "''"), strings.ReplaceAll(executable, "'", "''"))
	scriptFile, err := os.CreateTemp(filepath.Dir(executable), ".promptcat-upgrade-*.ps1")
	if err != nil {
		return fmt.Errorf("create Windows upgrade helper: %w", err)
	}
	scriptPath := scriptFile.Name()
	if _, err := scriptFile.WriteString(script); err != nil {
		scriptFile.Close()
		os.Remove(scriptPath)
		return fmt.Errorf("write Windows upgrade helper: %w", err)
	}
	if err := scriptFile.Close(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("close Windows upgrade helper: %w", err)
	}
	command := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-ExecutionPolicy", "Bypass", "-File", scriptPath)
	if err := command.Start(); err != nil {
		os.Remove(scriptPath)
		return fmt.Errorf("start Windows upgrade helper: %w", err)
	}
	if err := command.Process.Release(); err != nil {
		return fmt.Errorf("release Windows upgrade helper: %w", err)
	}
	return nil
}
