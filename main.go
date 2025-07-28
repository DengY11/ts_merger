package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const videoDir = "./videos"

type FileInfo struct {
	Name      string
	HasBak    bool
	StartTime float64
}

func getStartTime(filename string) float64 {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=start_time",
		"-of", "csv=p=0",
		filename,
	)
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("ffprobe错误 %s: %v\n", filename, err)
		return 0
	}
	startTimeStr := strings.TrimSpace(string(output))
	if startTimeStr == "" || startTimeStr == "N/A" {
		fmt.Printf("文件 %s 没有start_time信息\n", filename)
		return 0
	}
	if startTime, err := strconv.ParseFloat(startTimeStr, 64); err == nil {
		fmt.Printf("文件 %s start_time: %.6f\n", filename, startTime)
		return startTime
	}
	fmt.Printf("解析start_time失败 %s: %s\n", filename, startTimeStr)
	return 0
}

func isBakFile(filename string) bool {
	// 目前只检查了文件名是否含有“bak"
	return strings.Contains(strings.ToLower(filename), "bak")
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
	cmd := exec.Command("ffmpeg", "-f", "concat", "-safe", "0", "-i", listFile, "-c", "copy", "-y", output)
	return cmd.Run()
}

func mergeFinal(files []string, output string) error {
	fmt.Printf("开始最终合并，重新生成时间戳\n")
	var inputs []string
	var filterComplex string
	for i, file := range files {
		inputs = append(inputs, "-i", file)
		if i == 0 {
			filterComplex = fmt.Sprintf("[0:v] [0:a]")
		} else {
			filterComplex += fmt.Sprintf(" [%d:v] [%d:a]", i, i)
		}
	}
	filterComplex += fmt.Sprintf(" concat=n=%d:v=1:a=1 [v] [a]", len(files))
	args := []string{"-y"}
	args = append(args, inputs...)
	args = append(args, "-filter_complex", filterComplex)
	args = append(args, "-map", "[v]", "-map", "[a]")
	args = append(args, "-c:v", "libx264", "-preset", "fast", "-crf", "23")
	args = append(args, "-c:a", "aac", "-b:a", "128k")
	args = append(args, output)
	cmd := exec.Command("ffmpeg", args...)
	fmt.Printf("执行命令: %s\n", cmd.String())
	combinedOutput, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("FFmpeg输出:\n%s\n", string(combinedOutput))
		return err
	}
	fmt.Printf("合并成功\n")
	return nil
}

func copyFile(src, dst string) error {
	cmd := exec.Command("cp", src, dst)
	return cmd.Run()
}

func main() {
	// os.Remove("merged_normal.ts")
	// os.Remove("merged_bak.ts")
	// os.Remove("final_merged.mp4")
	entries, err := os.ReadDir(videoDir)
	if err != nil {
		log.Fatal("读取目录失败:", err)
	}
	var allFiles []FileInfo
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".ts") {
			fullPath := filepath.Join(videoDir, entry.Name())
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
	var normalOutput string
	if len(normalFiles) > 0 {
		normalOutput = "merged_normal.ts"
		fmt.Printf("合并普通文件到: %s\n", normalOutput)
		if err := mergeFiles(normalFiles, normalOutput); err != nil {
			log.Fatal("合并普通文件失败:", err)
		}
	}
	var bakOutput string
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
		cmd := exec.Command("ffmpeg", "-i", finalFiles[0], "-c", "copy", "-y", "final_merged.mp4")
		if err := cmd.Run(); err != nil {
			log.Fatal("转换MP4失败:", err)
		}
		fmt.Println("转换完成: final_merged.mp4")
		os.Remove(finalFiles[0])
	}
}
