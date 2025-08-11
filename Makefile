# s3-explorer 应用的 Makefile

# --- 变量定义 ---
# 设置你的应用程序名称
APP_NAME=s3-explorer
# Go 程序入口文件
MAIN_GO=main.go
# 存放构建产物的目录
BUILD_DIR=build

# Go 相关命令
GO=go
GO_BUILD=$(GO) build

# 编译标志
# 此标志用于在 Windows 上构建无控制台窗口的 GUI 应用
LDFLAGS_WINDOWS=-ldflags="-H windowsgui"

# --- 构建目标 ---

.PHONY: all build build-windows build-linux build-macos clean

# 默认目标: 为当前操作系统构建
build:
	@echo "为当前操作系统构建..."
	@mkdir -p $(BUILD_DIR)
	@if ($Env:OS -eq "Windows_NT") { \
    		$(GO_BUILD) -o $(BUILD_DIR)/$(APP_NAME).exe $(MAIN_GO); \
    	} else { \
    		$(GO_BUILD) -o $(BUILD_DIR)/$(APP_NAME) $(MAIN_GO); \
    	}

# 为所有平台构建
all: build-windows build-linux build-macos
	@echo "所有平台构建完成！"

# 交叉编译目标
build-windows:
	@echo "为 Windows (amd64) 构建..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=windows GOARCH=amd64 $(GO_BUILD) $(LDFLAGS_WINDOWS) -o $(BUILD_DIR)/$(APP_NAME).exe $(MAIN_GO)

build-linux:
	@echo "为 Linux (amd64) 构建..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=linux GOARCH=amd64 $(GO_BUILD) -o $(BUILD_DIR)/$(APP_NAME)-linux $(MAIN_GO)

build-macos:
	@echo "为 macOS (amd64) 构建..."
	@mkdir -p $(BUILD_DIR)
	@GOOS=darwin GOARCH=amd64 $(GO_BUILD) -o $(BUILD_DIR)/$(APP_NAME)-macos $(MAIN_GO)

# --- 清理任务 ---

clean:
	@echo "清理构建产物..."
	@rm -rf $(BUILD_DIR)