# 如何使用

## 编译
```go
go mod tidy 
go build -o merger main.go
```

## 运行
多个m3u8文件的url用空格分开, 脚本会自动从所给m3u8文件中提取出ts文件名, 从bucket中下载后合并
```go
./merger https://xxxxx.aliyun.com/xxxxx/sessionssss/aaaaaaa.m3u8 https://xxxxx.aliyun.com/xxxxx/sessionssss/bak.m3u8
```

