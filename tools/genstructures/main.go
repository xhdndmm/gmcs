// genstructures 从官方 jar 提取结构定义、structure_set、template_pool 与结构模板 NBT，
// 打包为 internal/world/vanilla/data/structures.tar.gz。
package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

func main() {
	jarPath := flag.String("jar", "", "Minecraft 服务端（或客户端）jar 路径")
	out := flag.String("out", "internal/world/vanilla/data/structures.tar.gz", "输出文件")
	flag.Parse()
	if *jarPath == "" {
		fmt.Fprintln(os.Stderr, "缺少 -jar 参数")
		os.Exit(2)
	}
	reader, err := zip.OpenReader(*jarPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "打开 jar 失败: %v\n", err)
		os.Exit(1)
	}
	defer reader.Close()
	files := make(map[string]*zip.File)
	for _, file := range reader.File {
		files[file.Name] = file
	}
	prefix := "META-INF/versions/1.21.11/"
	if _, ok := files[prefix+"data/minecraft/worldgen/structure/mineshaft.json"]; !ok {
		prefix = ""
	}
	base := prefix + "data/minecraft/"
	entries := make([]string, 0)
	for name := range files {
		if strings.HasPrefix(name, base+"worldgen/structure/") ||
			strings.HasPrefix(name, base+"worldgen/structure_set/") ||
			strings.HasPrefix(name, base+"worldgen/template_pool/") ||
			strings.HasPrefix(name, base+"structure/") {
			entries = append(entries, name)
		}
	}
	sort.Strings(entries)
	file, err := os.Create(*out)
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建输出文件失败: %v\n", err)
		os.Exit(1)
	}
	defer file.Close()
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	count := 0
	for _, name := range entries {
		key := strings.TrimPrefix(name, base)
		info := files[name]
		raw, err := readZipFile(info)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 %s 失败: %v\n", name, err)
			os.Exit(1)
		}
		header := &tar.Header{Name: path.Clean(key), Mode: 0o644, Size: int64(len(raw)), Typeflag: tar.TypeReg}
		if err := tarWriter.WriteHeader(header); err != nil {
			fmt.Fprintf(os.Stderr, "写入 tar 头失败: %v\n", err)
			os.Exit(1)
		}
		if _, err := tarWriter.Write(raw); err != nil {
			fmt.Fprintf(os.Stderr, "写入 %s 失败: %v\n", name, err)
			os.Exit(1)
		}
		count++
	}
	if err := tarWriter.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "关闭 tar 失败: %v\n", err)
		os.Exit(1)
	}
	if err := gzipWriter.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "关闭 gzip 失败: %v\n", err)
		os.Exit(1)
	}
	stat, _ := file.Stat()
	fmt.Printf("完成：%s（%d 个文件，%d 字节）\n", *out, count, stat.Size())
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}
