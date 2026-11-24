# MyLabel

MyLabel 是一款桌面端图像标注软件，用纯 Go 编写，基于 [mygo](https://github.com/egoist/mygo) 原生 UI（immediate-mode，GPU 渲染，无 WebView、无前端）。交互习惯参考 labelme，工具条布局参考 Photoshop。

支持目标检测与分割常见的三种标注形态：**矩形、多边形、旋转框（OBB）**，并可在 **YOLO / COCO / Pascal VOC** 三种数据集格式之间导入导出。

## 功能概览

- 打开图片文件夹即建工作区（递归扫描，标注存入工作区下的 SQLite 库 `mylabel.db`）
- 标注绘制与编辑：移动、缩放、旋转、顶点编辑、多选/框选、整组拖动、撤销/重做
- 标注上直接显示类别名标签；双击标签改类别，右键标注调整叠放次序
- 类别管理：增删改名换色；标注属性（自由键值对）
- 图片导航：上一张/下一张、完成状态标记、沿用上一张标注
- 导入标注：YOLO（det/seg/obb）、COCO、VOC，向导式类别映射
- 导出数据集：YOLO / COCO / VOC（含 VOC 分割掩码），支持 train/val 划分与逐图调整、有损转换 dry-run 报告
- 大图片分块加载（单边 >8000px）；万级图片工作区压测约 2.3s 完成索引
- 中英文界面

## 使用方法

1. 启动后点「打开文件夹」选择图片目录（也可以直接把文件夹拖进窗口）。
2. 在右侧类别面板添加类别（新工作区默认没有类别，需先添加才能画框）。
3. 在左侧工具条选择创建工具开始标注：矩形 (R)、多边形 (P)、旋转框 (O，依次点 4 个角)。
4. 标注完按 Ctrl+S 保存；用「导入标注 / 导出数据集」向导与其他格式互换。

常用快捷键：

| 快捷键 | 功能 |
| --- | --- |
| V / H / Z / M | 指针 / 抓手 / 缩放（再按 Z 切换放大缩小）/ 框选 |
| R / P / O | 矩形 / 多边形 / 旋转框 |
| Ctrl+滚轮 / 滚轮 / Shift+滚轮 | 缩放 / 垂直平移 / 水平平移 |
| Space+拖拽 或 中键拖拽 | 平移视图 |
| Ctrl+S / Ctrl+Z / Ctrl+Shift+Z | 保存 / 撤销 / 重做 |
| A / D | 上一张 / 下一张 |
| F | 适应窗口居中 |
| Ctrl+B | 沿用上一张标注 |
| Ctrl+D | 切换图片完成状态 |
| Ctrl+A / Delete / 数字键 1-9 | 全选 / 删除选中 / 改选中类别 |
| L | 显示/隐藏类别名标签 |

## 自己编译

需要 **Go 1.27.2+**（mygo 版本已锁定 v0.10.0，请勿升级）。

```bash
git clone https://github.com/kisaragychihaya/MyLabel.git
cd MyLabel
go mod download

# 开发模式（热重启）
go tool mygo dev

# 或直接运行
go run .

# 打包 Windows 产物（exe + NSIS 安装包）→ dist/windows-amd64/
GOFLAGS="-tags=mygo_nowebview" go tool mygo build
```

运行测试与检查：

```bash
go build ./... && go vet ./... && go tool mygo vet ./...
go tool mygo i18n check
go test ./... -count=1
```

## 技术栈

- 语言：Go
- UI：`github.com/egoist/mygo`（v0.10.0，原生 immediate-mode UI）
- 存储：`modernc.org/sqlite`（纯 Go，无 cgo）

更详细的架构与开发约定见 [AGENTS.md](AGENTS.md) 与 [开发计划书.md](开发计划书.md)。
