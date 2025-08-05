package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const (
	videoDir = "./videos"
	tempDir  = "./temp"
)

type FileInfo struct {
	Name      string
	HasBak    bool
	StartTime float64
}

type FFProbeFormat struct {
	StartTime string `json:"start_time"`
}

type FFProbeResult struct {
	Format FFProbeFormat `json:"format"`
}

func main() {
	if len(os.Args) > 1 {
		fmt.Println("使用远程模式")
		os.RemoveAll(tempDir)
		if err := os.MkdirAll(tempDir, 0755); err != nil {
			log.Fatal("创建临时目录失败:", err)
		}
		for i := 1; i < len(os.Args); i++ {
			fileURL := os.Args[i]
			fmt.Printf("处理URL: %s\n", fileURL)
			if err := downloadFromCDN(fileURL); err != nil {
				fmt.Printf("警告: 下载文件失败: %v，跳过\n", err)
				continue
			}
		}
		processVideoFiles(tempDir)
		defer os.RemoveAll(tempDir)
	} else {
		fmt.Println("使用本地模式")
		processVideoFiles(videoDir)
	}
}

func downloadFile(url, localPath string) error {
	fmt.Printf("下载文件: %s\n", filepath.Base(url))
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}
	resp, err := http.Get(url)
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

func downloadFromCDN(fileURL string) error {
	fmt.Printf("开始下载文件: %s\n", fileURL)
	isBak := strings.Contains(fileURL, "/bak/")
	fileName := filepath.Base(fileURL)
	if fileName == "" {
		return fmt.Errorf("无效的文件URL: %s", fileURL)
	}
	localFileName := fileName
	if isBak && !strings.HasPrefix(fileName, "bak_") {
		localFileName = "bak_" + fileName
	}
	localPath := filepath.Join(tempDir, localFileName)
	fileType := "主"
	if isBak {
		fileType = "备份"
	}
	fmt.Printf("下载%s文件: %s\n", fileType, fileName)
	if err := downloadFile(fileURL, localPath); err != nil {
		return fmt.Errorf("下载文件失败: %v", err)
	}
	fmt.Printf("文件下载完成: %s\n", localFileName)
	return nil
}

func getStartTime(filename string) float64 {
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
		fmt.Printf("文件 %s 没有start_time信息\n", filename)
		return 0
	}
	if startTime, err := strconv.ParseFloat(startTimeStr, 64); err == nil {
		fmt.Printf("文件 %s start_time: %.6f\n", filename, startTime)
		return startTime
	} else {
		fmt.Printf("文件 %s start_time解析失败: %s\n", filename, startTimeStr)
		return 0
	}
}

func isBakFile(filename string) bool {
	return strings.Contains(strings.ToLower(filename), "bak")
}

func copyFile(src, dst string) error {
	return ffmpeg.Input(src).
		Output(dst, ffmpeg.KwArgs{"c": "copy"}).
		OverWriteOutput().
		Run()
}

func mergeFiles(files []FileInfo, output string) error {
	if len(files) == 1 {
		return copyFile(files[0].Name, output)
	}
	listFile := "temp_list.txt"
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
		Output(output, ffmpeg.KwArgs{"c": "copy"}).
		OverWriteOutput().
		Run()
}

func mergeFinal(files []string, output string) error {
	fmt.Printf("开始最终合并，重新生成时间戳\n")
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
		Output(output, ffmpeg.KwArgs{
			"c:v":    "libx264",
			"preset": "fast",
			"crf":    "23",
			"c:a":    "aac",
			"b:a":    "128k",
		}).
		OverWriteOutput().
		Run()
	if err != nil {
		return err
	}
	fmt.Printf("合并成功\n")
	return nil
}

func processVideoFiles(sourceDir string) {
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		log.Fatal("读取目录失败:", err)
	}
	var allFiles []FileInfo
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".ts") {
			fullPath := filepath.Join(sourceDir, entry.Name())
			fmt.Printf("分析文件: %s\n", fullPath)
			startTime := getStartTime(fullPath)
			allFiles = append(allFiles, FileInfo{
				Name:      fullPath,
				HasBak:    isBakFile(entry.Name()),
				StartTime: startTime,
			})
		}
	}
	if len(allFiles) == 0 {
		log.Fatal("没有找到TS文件")
	}
	var normalFiles, bakFiles []FileInfo
	for _, file := range allFiles {
		if file.HasBak {
			bakFiles = append(bakFiles, file)
		} else {
			normalFiles = append(normalFiles, file)
		}
	}
	sort.Slice(normalFiles, func(i, j int) bool {
		return normalFiles[i].StartTime < normalFiles[j].StartTime
	})
	sort.Slice(bakFiles, func(i, j int) bool {
		return bakFiles[i].StartTime < bakFiles[j].StartTime
	})
	fmt.Printf("普通文件: %d 个\n", len(normalFiles))
	for i, file := range normalFiles {
		fmt.Printf("  %d. %s (开始时间: %.6f)\n", i+1, filepath.Base(file.Name), file.StartTime)
	}
	fmt.Printf("迁移文件: %d 个\n", len(bakFiles))
	for i, file := range bakFiles {
		fmt.Printf("  %d. %s (开始时间: %.6f)\n", i+1, filepath.Base(file.Name), file.StartTime)
	}
	var normalOutput, bakOutput string
	if len(normalFiles) > 0 {
		normalOutput = "merged_normal.ts"
		fmt.Printf("合并普通文件到: %s\n", normalOutput)
		if err := mergeFiles(normalFiles, normalOutput); err != nil {
			log.Fatal("合并普通文件失败:", err)
		}
	}
	if len(bakFiles) > 0 {
		bakOutput = "merged_bak.ts"
		fmt.Printf("合并迁移文件到: %s\n", bakOutput)
		if err := mergeFiles(bakFiles, bakOutput); err != nil {
			log.Fatal("合并迁移文件失败:", err)
		}
	}
	var finalFiles []string
	if normalOutput != "" {
		finalFiles = append(finalFiles, normalOutput)
	}
	if bakOutput != "" {
		finalFiles = append(finalFiles, bakOutput)
	}
	if len(finalFiles) > 1 {
		fmt.Println("合并最终文件...")
		if err := mergeFinal(finalFiles, "final_merged.mp4"); err != nil {
			log.Fatal("最终合并失败:", err)
		}
		fmt.Println("最终合并完成: final_merged.mp4")
		for _, file := range finalFiles {
			os.Remove(file)
		}
	} else if len(finalFiles) == 1 {
		fmt.Println("转换为MP4...")
		err := ffmpeg.Input(finalFiles[0]).
			Output("final_merged.mp4", ffmpeg.KwArgs{"c": "copy"}).
			OverWriteOutput().
			Run()
		if err != nil {
			log.Fatal("转换MP4失败:", err)
		}
		fmt.Println("转换完成: final_merged.mp4")
		os.Remove(finalFiles[0])
	}
}
