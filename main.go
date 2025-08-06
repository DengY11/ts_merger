package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const tempDir = "./temp"

type FileInfo struct {
	Name      string
	Group     string
	StartTime float64
}

type FFProbeFormat struct {
	StartTime string `json:"start_time"`
}

type FFProbeResult struct {
	Format FFProbeFormat `json:"format"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: ./simple-merger <m3u8_url1> [m3u8_url2] ...")
		os.Exit(1)
	}
	os.RemoveAll(tempDir)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		log.Fatal("创建临时目录失败:", err)
	}
	defer os.RemoveAll(tempDir)
	var allFiles []FileInfo
	for i := 1; i < len(os.Args); i++ {
		m3u8URL := os.Args[i]
		fmt.Printf("处理M3U8: %s\n", m3u8URL)
		tsFiles, err := parseM3U8(m3u8URL)
		if err != nil {
			fmt.Printf("警告: 解析M3U8失败: %v，跳过\n", err)
			continue
		}
		fmt.Printf("找到 %d 个TS文件\n", len(tsFiles))
		if len(tsFiles) > 0 {
			fmt.Printf("第一个TS文件: %s\n", tsFiles[0])
			if len(tsFiles) > 1 {
				fmt.Printf("最后一个TS文件: %s\n", tsFiles[len(tsFiles)-1])
			}
		}
		for _, tsURL := range tsFiles {
			fileName := filepath.Base(tsURL)
			group := getFileGroup(fileName)
			localPath := filepath.Join(tempDir, fileName)
			fmt.Printf("下载文件: %s (分组: %s)\n", fileName, group)
			if err := downloadFile(tsURL, localPath); err != nil {
				fmt.Printf("警告: 下载失败: %v，跳过\n", err)
				continue
			}
			startTime := getStartTime(localPath)
			allFiles = append(allFiles, FileInfo{
				Name:      localPath,
				Group:     group,
				StartTime: startTime,
			})
		}
	}
	if len(allFiles) == 0 {
		log.Fatal("没有成功下载任何文件")
	}
	processAndMerge(allFiles)
}

func createHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
	}
}

func parseM3U8(m3u8URL string) ([]string, error) {
	client := createHTTPClient()
	req, err := http.NewRequest("GET", m3u8URL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("获取M3U8文件失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("M3U8文件下载失败，状态码: %d", resp.StatusCode)
	}
	var tsFiles []string
	scanner := bufio.NewScanner(resp.Body)
	baseURL, _ := url.Parse(m3u8URL)
	baseURL.Path = filepath.Dir(baseURL.Path)
	if !strings.HasSuffix(baseURL.Path, "/") {
		baseURL.Path += "/"
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasSuffix(line, ".ts") {
			var tsURL string
			if strings.HasPrefix(line, "http") {
				tsURL = line
			} else {
				tsURL = baseURL.String() + line
			}
			tsFiles = append(tsFiles, tsURL)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取M3U8内容失败: %v", err)
	}
	return tsFiles, nil
}

func getFileGroup(fileName string) string {
	if strings.HasPrefix(fileName, "bak") {
		re := regexp.MustCompile(`^(bak\d+)_`)
		matches := re.FindStringSubmatch(fileName)
		if len(matches) > 1 {
			return matches[1]
		}
		return "bak"
	}
	return "main"
}

func downloadFile(url, localPath string) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}
	client := createHTTPClient()
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Connection", "keep-alive")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败，状态码: %d", resp.StatusCode)
	}
	file, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("创建本地文件失败: %v", err)
	}
	defer file.Close()
	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return fmt.Errorf("保存文件失败: %v", err)
	}
	return nil
}

func getStartTime(filename string) float64 {
	baseName := filepath.Base(filename)
	re := regexp.MustCompile(`_testpagerec(\d+)_(\d+)\.ts$`)
	matches := re.FindStringSubmatch(baseName)
	if len(matches) >= 3 {
		if startTime, err := strconv.ParseFloat(matches[1], 64); err == nil {
			return startTime / 1000.0
		}
	}
	output, err := ffmpeg.Probe(filename)
	if err != nil {
		fmt.Printf("ffprobe错误 %s: %v\n", filename, err)
		return 0
	}
	var result FFProbeResult
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		fmt.Printf("JSON解析错误 %s: %v\n", filename, err)
		return 0
	}
	startTimeStr := result.Format.StartTime
	if startTimeStr == "" || startTimeStr == "N/A" {
		return 0
	}
	if startTime, err := strconv.ParseFloat(startTimeStr, 64); err == nil {
		return startTime
	}
	return 0
}

func copyFile(src, dst string) error {
	return ffmpeg.Input(src).
		Output(dst, ffmpeg.KwArgs{"c": "copy"}).
		OverWriteOutput().
		Run()
}

func mergeFiles(files []FileInfo, output string) error {
	if len(files) == 0 {
		return fmt.Errorf("没有文件要合并")
	}
	if len(files) == 1 {
		return copyFile(files[0].Name, output)
	}
	fmt.Printf("    使用无损合并模式\n")
	listFile := fmt.Sprintf("temp_list_%s.txt", strings.ReplaceAll(output, "/", "_"))
	f, err := os.Create(listFile)
	if err != nil {
		return err
	}
	for _, file := range files {
		fmt.Fprintf(f, "file '%s'\n", file.Name)
	}
	f.Close()
	defer os.Remove(listFile)
	return ffmpeg.Input(listFile, ffmpeg.KwArgs{
		"f":    "concat",
		"safe": "0",
	}).
		Output(output, ffmpeg.KwArgs{
			"c":                 "copy",
			"avoid_negative_ts": "make_zero",
		}).
		OverWriteOutput().
		Run()
}

func mergeFinal(files []string, output string) error {
	if len(files) == 0 {
		return fmt.Errorf("没有文件要最终合并")
	}
	if len(files) == 1 {
		fmt.Printf("单文件转换为MP4容器（无损）\n")
		return ffmpeg.Input(files[0]).
			Output(output, ffmpeg.KwArgs{
				"c":        "copy",
				"movflags": "faststart",
			}).
			OverWriteOutput().
			Run()
	}
	fmt.Printf("多文件无损合并\n")
	tempMerged := "temp_merged_all.ts"
	listFile := "temp_final_list.txt"
	f, err := os.Create(listFile)
	if err != nil {
		return err
	}
	for _, file := range files {
		fmt.Fprintf(f, "file '%s'\n", file)
	}
	f.Close()
	defer os.Remove(listFile)
	err = ffmpeg.Input(listFile, ffmpeg.KwArgs{
		"f":    "concat",
		"safe": "0",
	}).
		Output(tempMerged, ffmpeg.KwArgs{
			"c": "copy",
		}).
		OverWriteOutput().
		Run()
	if err != nil {
		return fmt.Errorf("TS文件合并失败: %v", err)
	}
	defer os.Remove(tempMerged)
	fmt.Printf("转换为MP4容器（保持原始质量）\n")
	return ffmpeg.Input(tempMerged).
		Output(output, ffmpeg.KwArgs{
			"c":                 "copy",
			"movflags":          "faststart",
			"avoid_negative_ts": "make_zero",
		}).
		OverWriteOutput().
		Run()
}

func processAndMerge(allFiles []FileInfo) {
	groupedFiles := make(map[string][]FileInfo)
	for _, file := range allFiles {
		groupedFiles[file.Group] = append(groupedFiles[file.Group], file)
	}
	fmt.Printf("发现 %d 个分组:\n", len(groupedFiles))
	for group, files := range groupedFiles {
		fmt.Printf("  %s: %d个文件\n", group, len(files))
	}
	var mergedFiles []string
	groupOrder := []string{"main"}
	for i := 0; i < 10; i++ {
		groupName := fmt.Sprintf("bak%d", i)
		if _, exists := groupedFiles[groupName]; exists {
			groupOrder = append(groupOrder, groupName)
		}
	}
	if _, exists := groupedFiles["bak"]; exists {
		groupOrder = append(groupOrder, "bak")
	}
	for _, group := range groupOrder {
		files, exists := groupedFiles[group]
		if !exists {
			continue
		}
		sort.Slice(files, func(i, j int) bool {
			return files[i].StartTime < files[j].StartTime
		})
		fmt.Printf("\n合并%s组 (%d个文件):\n", group, len(files))
		for i, file := range files {
			fmt.Printf("  %d. %s (时间: %.6f)\n", i+1, filepath.Base(file.Name), file.StartTime)
		}
		groupOutput := fmt.Sprintf("merged_%s.ts", group)
		fmt.Printf("合并到: %s\n", groupOutput)
		if err := mergeFiles(files, groupOutput); err != nil {
			log.Printf("合并%s组失败: %v", group, err)
			continue
		}
		mergedFiles = append(mergedFiles, groupOutput)
	}
	if len(mergedFiles) == 0 {
		log.Fatal("没有成功合并任何组")
	}
	fmt.Printf("\n=== 最终合并阶段 ===\n")
	fmt.Printf("准备合并 %d 个组文件:\n", len(mergedFiles))
	for i, file := range mergedFiles {
		fmt.Printf("  %d. %s\n", i+1, file)
	}
	finalOutput := "final_merged.mp4"
	fmt.Printf("\n开始生成最终MP4文件: %s\n", finalOutput)
	fmt.Printf("使用无损模式，保持最高质量...\n")
	if err := mergeFinal(mergedFiles, finalOutput); err != nil {
		log.Fatal("最终合并失败:", err)
	}
	fmt.Printf("清理临时文件...\n")
	for _, file := range mergedFiles {
		os.Remove(file)
	}
	fmt.Printf("\n合并完成! 输出文件: %s\n", finalOutput)
	fmt.Printf("文件保存在当前目录: %s\n", finalOutput)
	if info, err := os.Stat(finalOutput); err == nil {
		fmt.Printf("📊 文件大小: %.2f MB\n", float64(info.Size())/(1024*1024))
	}
}
