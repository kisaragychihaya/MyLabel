# AGENTS.md — MyLabel

图像标注桌面软件（PoC 已完成，进入正式开发阶段）。技术路线：**mygo 原生 UI**（`ui` 包，纯 Go immediate-mode，GPU 渲染，无 WebView、无前端、无 Bun）。

## 技术栈与版本

- Go 1.27.2+，`github.com/egoist/mygo` **锁定 v0.10.0**（pre-1.0 框架，升级作为独立任务评估，不要盲目 `go get -u`）
- 存储：SQLite 工作库 `<图片根>/mylabel.db`（`internal/store`，`modernc.org/sqlite` 纯 Go 无 cgo，WAL）。遗留 `mylabel.annotations.json` 只作交换格式："导出 JSON"按钮 + 打开目录时一次性自动迁移（不删旧文件）
- 图片导入：`internal/imaging`（递归扫描 → JPEG EXIF 物理转正 → 尺寸 → sha256）；尺寸探测用 `image.DecodeConfig`（已注册 jpeg/png/gif + `golang.org/x/image` webp/bmp）
- mygo 框架源码与中文文档：`C:\Users\admin\go\pkg\mod\github.com\egoist\mygo@v0.10.0`（`docs-zh/ui/*.md`、`examples/`）——遇到 API 问题先查这里，不要猜

## 常用命令

```bash
go tool mygo dev          # 开发（热重启，不需 Bun）
go build ./...            # 编译
go vet ./...              # 静态检查
go tool mygo vet ./...    # mygo immediate-mode 误用检查（必须过）
go test ./... -count=1    # 全部测试（必须全绿）
go test -bench=Benchmark -run='^$' ./internal/app/   # 绘制性能
GOFLAGS="-tags=mygo_nowebview" go tool mygo build    # 出 Windows exe + NSIS 安装包 → dist/windows-amd64/
go tool mygo build -platform web                     # WASM 浏览器版 → dist/web/
go run ./cmd/genimg -dir ./testdata/imgs -n 500      # 生成测试图片（-big 出 8192/10000 大图）
```

产物 `dist/`、测试数据 `testdata/` 不入库。

## 代码结构

```
main.go                 入口：WhenReady + NewWindow(Content: ui.View(a.View)) + OnClose(关闭守卫) + App.Run()
internal/app/           全部应用代码（单包）
  app.go                App 状态结构 + 主视图装配（工具栏/画布+类别面板/缩略图条/状态栏）、openDir（YOLO 子目录上提到数据集根→扫描→入库→迁移→加载）
  model.go              Ann（可旋转矩形，含 Attributes map）/ImageInfo/docFile/交互模式枚举、Category 别名、Ann↔store.AnnRec 转换
  category.go           类别面板 + 12 色色板 Modal + 生成式调色板（黄金角 HSV）/增删改名换色/删除保护（cmdDeleteCategory）；新工作区类别为空，空表时面板显示提示、curCat=-1（clampCurCat）
  status.go             图片状态：Ctrl+D 切换、首次编辑自动 new→labeling、缩略图圆点颜色
  canvas.go             画布：Box + 单 Draw 回调绘制全部标注（禁止一个标注一个 Element）；poly 用 FillPath+StrokePolyline；类别名 chip 画在画布 DIP 层（≤500 条全画，>500 只画选中），尺寸由 canvasView 每帧按类别名测一次缓存进 App（chipBounds 是绘制与命中共用的唯一几何）
  interact.go           HandleInput 交互状态机（rect/poly/obb 创建、移动/resize/旋转、顶点编辑、框选、整组移动、快捷键、双击开属性、chip 命中、右键 z 序菜单）
  geom.go               纯几何：局部坐标变换、手柄位置、zoomAroundPoint、resizeHandle/rotateTo、annBounds、obbFromPoints
  polygon.go            多边形纯几何：pointInPolygon/nearestEdge/顶点增删/平移（全部返回新切片，不改入参）
  hit.go                命中测试：顶点 > 手柄 > 边 > 内部（poly 用 pointInPolygon），z 序倒序；chipHitAt 只命中画出来的 chip
  undo.go               命令栈（200 条上限）+ cmdCreate/cmdDelete/cmdModify/cmdReorder + 批量 cmdBatchSet/cmdBatchDelete/cmdDeleteCategory（Command.Img 指明所属图片；Ann 含 slice，快照必须 cloneAnn 深拷贝）
  thumbs.go             缩略图 worker 池（4 goroutine + generation 防串代 + win.Update 回主线程；无窗口不启动）
  tiles.go              大图分块（单边 >8000px 触发，tileSize 2048，overview 概览图）、loadImage
  persist.go            save 走 store（只写 dirtyImages 的图 + 类别）、exportJSON、loadDoc、遗留 JSON 迁移
  irconv.go             app↔IR 转换（rect/poly/rotated_bbox、category key 去重、多环与 attributes 有损计数）
  importapply.go        importer.Dataset → store 的应用管线（类别映射 resolveChoices、applyImport、一条批量 cmdImport undo）
  importwizard.go       "导入标注"向导：格式→路径→映射→报告 四页状态机 + Modal UI；无工作区时按来源路径推导图片根并自动 openDir（parse 前完成）
  exportconv.go         app→IR 装配（会话内存状态为准，不强制先保存）：类别 appCategoriesToIR、标注 annsToIR、File 用 RelPath 同 cleanImportPath 规则、带 store 的 sha256、inferExportTask 按内容推 det/seg/obb
  exportwizard.go       "导出数据集"向导：格式(任务/VOC 选项)→split→BuildPlan dry-run 报告→输出目录 四页 + 后台 WriteExport 进度（win.Update，无窗口内联跑）+ Toast
  openir.go             openDir 时根下 project.json 的直接导入（优先于遗留 JSON，库中已有标注则跳过）
  opendir.go            resolveOpenDir：Ultralytics 布局 X/images/train 或 X/images 自动上提到数据集根 X（需同级 labels/），不递归上提
  attributes.go         标注属性面板：双击单选标注打开，暂存类别+属性键值对，确定一次性提交为一条 undo
  annmenu.go            作用于单条标注的两个菜单（App 字段 catPick/zMenu，按 Image+ID 认人）：双击 chip = 类别单选列表（点一行即应用，一条 cmdModify）；右键标注 = z 序菜单（上移一层/下移一层/移到顶部/移到底部，一条 cmdReorder，到顶/到底的项 Disabled），面板用 ui.Popover + ui.Button
  close.go              ConfirmClose 关闭守卫（保存失败才弹三选；无窗口直接返回 save 结果）
  drop.go               拖文件/文件夹进窗口打开
internal/store/         SQLite 仓储（categories/images/annotations 三表，schema v4 含 attrs，非并发安全，只在主线程用）；标注顺序 = Anns 切片序（= 绘制序），LoadImages 用 ORDER BY rowid 读、ReplaceAnns 按切片序插入，故保存/重开保持顺序（改这两处会破坏 z 序）
internal/imaging/       递归扫描 + JPEG EXIF 物理转正 + 尺寸 + sha256
internal/ir/            IR 交换模型（bbox/polygon(rings)/rotated_bbox + attributes）、Validate/Normalize、project.json + ir/*.json 读写
internal/importer/      YOLO（det/seg/obb 按字段数+Task 裁决；接受数据集根目录，labels↔images 镜像匹配，data.yaml 或唯一 *.yaml 取类名）/VOC（robndbox 优先、1-based 选项）/COCO（segmentation 联合类型、RLE 两种 counts 降级 bbox）→ IR
internal/exporter/      IR → YOLO/COCO/VOC 数据集目录：BuildPlan（纯计算 dry-run，Report 含 Lossy 规则计数+示例）→ WriteExport（临时目录组装→原子改名，已存在目录报错不覆盖，progress 回调）
cmd/genimg/             测试图片生成器
docs/poc-design.md      PoC 架构设计文档
docs/phase1-design.md   阶段 1（数据层）契约
docs/phase2-design.md   阶段 2（seg/多选/类别面板/状态标记）契约
docs/phase3-design.md   阶段 3（IR/导入器/属性/obb 四点）契约
开发计划书.md           总体开发计划与进度追踪
```

## 铁律（违反会被 QC 打回，`mygo vet` 能发现一部分）

1. `ui.Element` **只在一次构建内有效**：不得存入结构体字段、包变量、goroutine。持久身份用 `ui.Handle`，跨帧状态放 App 字段。
2. 后台 goroutine 改状态必须 `win.Update(func(){...})`；只重绘用 `win.Invalidate()`。
3. `Hovered()` / `PointerPosition()` 只允许在 `Draw` 回调里读。
4. GridView/List 行滚出视口会被回收，缓存（如缩略图）放 App 状态，不放行元素。
5. 标注渲染永远在一个 `Box` 的 `Draw` 回调里循环，**禁止一个标注一个 Element**（帧开销随元素数线性增长）。
6. 同一父元素下 Key 不得重复（严格模式 panic）。
7. 不要新增第三方依赖而不先讨论；不要动 go.mod 的 mygo 版本。

## 坐标系（改画布代码前必读）

- **世界坐标 = 图像绝对像素**；屏幕坐标 = canvas 元素本地 DIP。IR/持久化只存图像像素。
- 指针 → 图像：`HandleInput` 交付的 `ev.X/Y` **已是元素本地 DIP**（不要再用 WindowToLocal，会重复减原点）；`imgPt = (ev - pan) / zoom`。
- 图像 → 屏幕：`screen = pan + img * zoom`。
- 图像层绘制用 `ui.Scaling(z).Then(ui.Translation(pan+origin))`；标注/手柄层在画布局部 DIP 下用**预旋转顶点**（`corners()`）+ `StrokePolyline`——不要用旋转变换的 rect op（CPU 光栅下慢约 20 倍）。
- 缩放任一锚点用 `zoomAroundPoint`（纯函数，光标下图像点不动）；产品决策：Ctrl+滚轮以**图片中心**为锚点。
- 中键 = Button 2；滚轮向用户一格 DY=+99（Windows 语义，平移用 `pan -= D`）。

## 交互约定（labelme 风格，改动需保持）

Space+左键拖拽 = 抓手平移；滚轮 = 垂直平移；Shift+滚轮 = 水平平移；Ctrl+滚轮 = 缩放；中键拖拽 = 平移；左键空白拖拽 = 创建框（ToolRect）或加点（ToolPoly/ToolObb）（空类别表下三种工具都不开草稿，提示「请先在右侧面板添加类别」）；R/P/O = 切换矩形/多边形/旋转框工具；旋转框 = 点 4 个角成框（第 4 点即成，Esc 取消、Backspace 删点）；多边形：单击加点、点首顶点/回车/双击闭合（≥3 点）、Esc 取消、Backspace 删末点、拖顶点编辑、Ctrl+点删顶点、双击边插顶点、拖内部整体移动；双击单选标注（rect 任意处、poly 内部）= 属性面板；类别名 chip = 独立控件，优先于其下标注的一切（单选 = 选中该条、双击 = 类别单选列表弹窗，点一行立即改类别并关闭，Esc/取消不改；标签隐藏或 >500 条未选中时不参与命中）；右键标注 = z 序菜单（上移一层/下移一层/移到顶部/移到底部 各一条 undo，已到顶/到底的项置灰；手势或草稿进行中右键无反应，点空白处不弹，点外面/Esc 关闭且不选中、不建草稿）；Ctrl+点击 = 多选切换；Shift+空白拖拽 = 框选；Ctrl+A = 全选；Delete/1-9 作用于全部选中（各一条批量 undo）；拖任一选中项 = 整组平移；resize/旋转/顶点编辑仅单选可用；Esc = 先取消手势再清空选择；Ctrl+D = 标记当前图 done/new；Ctrl+S/Z/Shift+Z = 保存/撤销/重做；L 或工具栏「标签」= 显示/隐藏类别名 chip（默认显示）。加载图片后自动"适应窗口居中"并把键盘焦点移到画布。

## 测试约定

- `ui.Tester` 有线程泄漏（mygo issue #1902）：**一个测试函数只建一个 Tester，禁止 time.Sleep，动画用 `tt.Advance`**。
- 建 Tester 的 App 若打开了目录（`openDir`），用 `t.Cleanup(a.Close)` 释放 SQLite，否则 Windows 上 `t.TempDir` 清理会因句柄未释放失败。
- 关闭守卫用无窗口路径测（`App.win == nil` 时 `ConfirmClose` 不弹窗，直接返回 `save() == nil`）。
- 坐标/绘制正确性用像素级断言（`tt.Image()` + 标记图），几何纯函数用往返误差断言（<1e-9）。
-  Tester 的滚轮/按键不带修饰键：测 Ctrl/Shift 组合时直接调 `handleCanvasInput` 构造 `ui.InputEvent`。
- Tester 的连击计数是**包内共享、跨测试保留**的（同一个 Tester 复用，时钟只有 `tt.Advance` 才走）：测试开头先 `tt.Advance` 断开上一次的点击序列，否则第一次点击会带着 Clicks=2 进来（会被当成双击）。
- 修改交互行为必须同步更新对应回归测试（见 `interact_test.go` 命名风格 `TestXxxYyy`）。

## 已知限制（PoC 遗留，见开发计划书 §6 阶段 0 记录）

- 类别数量不限（面板可增删改名换色），且**新工作区不预置任何类别**（cat1..cat9 只是测试 fixture）：空表是合法状态，此时三种创建工具被拦截并提示「请先在右侧面板添加类别」，数字键无动作；`defaultCategoryList()` 仅供测试注入。`images.status` 只有 new/labeling/done 在 UI 里可达，reviewed/skipped 仅保留枚举
- 类别颜色只能从 12 个预设里选（无自由取色器）；`exportJSON` 的 PoC 格式只写类别名，颜色只在工作库里
- 分块大图整图常驻内存（未做按视野懒加载）
- 标注属性是自由文本键值对（schema v4 的 `annotations.attrs`）：没有类型、没有枚举、没有按属性筛选；改类别与改属性经属性面板一次性提交为一条 undo，面板里的类别选择在"确定"前不落库
- 目录扫描已递归（`imaging.Scan`），但无文件监视；`exportJSON` 仍用 Remove+Rename（Windows 无原子覆盖，PoC 接受）
- 遗留 JSON 只在"库中所有图片都没有标注"时自动迁移一次，且不删旧文件；匹配按裸文件名或唯一 base 名；PoC 文档的 ann ID 跨会话复用，导入时按图去重（`ensureUniqueAnnIDs`），加载后 `seedAnnIDCounter` 防止新 ID 撞车
- 5000+ 标注时 CPU 光栅掩码图集是瓶颈（真机 GPU 无此问题）
- 万级图片与 >8000px 大图的专项压测未做（测试数据生成命令见上）
