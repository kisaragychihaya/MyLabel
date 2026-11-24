# 阶段 5 设计契约：打磨（i18n / 导航与沿用 / 视觉回归 / 导出三遗留）

绑定文档。阶段 4 的三个遗留项（导出取消、VOC seg PNG、split 手动逐图调整）经用户确认并入本阶段；三平台打包与 WASM 演示版本阶段不做。

波次划分（每波一次 QC 门禁 + 一次 git 提交）：

- **W1**（app）：§1 图片导航、§2 标注沿用、§3 快捷键补齐。
- **W2**（app，与 W1 并行）：§4 `tt.Image()` 视觉回归，只新增 `internal/app/visual_test.go`。
- **W3a**（exporter + 编译适配）：§5 WriteExport 可取消（ctx）、§6 VOC seg PNG 栅格化、§7 SplitPolicy.Overrides。含 app 侧机械适配（仅让编译通过，不加 UI）。
- **W3b**（app，在 W3a 之后）：§5 导出向导取消按钮、§7 逐图调整对话框。
- **W4**（app 全面）：§8 i18n 消息目录。
- **W5**（主代理）：§9 性能回归 + 计划书/AGENTS.md 收尾。

QC 门禁（每波必过）：`go build ./... && go vet ./... && go tool mygo vet ./... && go test ./... -count=1` 全绿；W4 额外 `go tool mygo i18n check`。

---

## §1 图片导航（上一张/下一张，labelme 习惯）

- 新方法：`(a *App) goImage(delta int) bool`，放 `interact.go` 附近或新 `nav.go`。
  - 无图片（`len(a.images) == 0`）返回 false。
  - `target := a.selImg + delta`，越界时置 `a.lastStatus = "已经是第一张"` / `"已经是最后一张"`，返回 false。
  - `a.dragInProgress()` 为真时返回 false（与 Delete 守卫同理：手势持有索引快照）。
  - 否则 `a.selImg = target`，返回 true。加载、清选择、关属性面板/菜单等由 `thumbStrip` 既有的 selImg≠loaded 调和逻辑完成，**不要复制那份逻辑**。多边形草稿被丢弃与点击缩略图切图行为一致，属于既有语义，不改。
- 快捷键（`keyDown`，要求 `ev.Mods == 0`，防止与 Ctrl+A/Ctrl+D 组合键双重触发）：
  - `A` = `goImage(-1)`，`D` = `goImage(+1)`。
- 工具栏提示文字追加「A/D 切换图片」（i18n 前先用中文字面量，W4 统一收编）。

## §2 标注沿用（沿用上一张）

- 新文件 `internal/app/carry.go`：`(a *App) carryPrevImage() bool`。
  - 前置：`a.loaded >= 1` 且未在拖动手势中（`dragInProgress()`）；否则 false（`loaded < 1` 时置 `lastStatus = "没有上一张图片"`）。
  - 源 = `a.images[a.loaded-1].Anns`，为空时置 `lastStatus = "上一张没有标注可沿用"`，返回 false，**不产生 undo 记录**。
  - 拷贝规则：逐条 `cloneAnn` 深拷贝（含 Points 切片与 Attributes map），经现有 ID 分配器换新 ID（不得与当前图已有 ID 冲突），按源顺序**追加**到当前图 Anns 末尾（z 序保持源相对顺序，整体置于现有标注之上）。类别是全局索引，直接沿用。
  - 一条 undo：仿照 `cmdImport`（importapply.go）的批量创建模式，新增 `cmdCarry`（undo 移除整批、redo 重新追加整批，按 ID 认人）。
  - 副作用：沿用是一次编辑 → `setDirty` + 首次编辑状态流转（new→labeling）应自动发生（与 push 路径一致，若现状是 push 不触发流转则与 cmdImport 对齐，两者行为必须一致）。
  - 完成后选中新沿用的整批（`selSet` 重建为新增索引集），`lastStatus = fmt.Sprintf("已从上一张沿用 %d 条标注", n)`。
- 快捷键：`Ctrl+B`（在 `keyDown` 处理，判定 `ev.Mods&ui.Ctrl != 0 && ev.Key == ui.KeyB`，先于单键 switch）。
- 工具栏提示文字追加「Ctrl+B 沿用上一张」。

## §3 快捷键补齐

- `F`（`ev.Mods == 0`）：重新执行「适应窗口居中」（与加载图片后的 fit 同一份代码，抽成 `(a *App) fitWindow()` 复用；无图片时 false）。
- 既有快捷键不变。改完后 `AGENTS.md` 交互约定一节追加：A/D 切图、Ctrl+B 沿用、F 适应窗口。

## §4 视觉回归（`internal/app/visual_test.go`，全新文件）

遵循 AGENTS.md 测试约定：一个测试函数一个 Tester、禁 time.Sleep、开头 `tt.Advance` 断连击、断言**形状与颜色像素**而非文字字形（字形跨机器不稳定，chip 只断言背景色块区域、避开文字区）。固定窗口尺寸（Tester 默认即可，场景内显式设 zoom/pan 使坐标确定）。

场景（每场景一个测试函数，独立 Tester）：

1. `TestVisualRectSelected`：单矩形选中态——框线颜色、8 手柄像素、未选中区域无手柄色。
2. `TestVisualObbRotated`：30° 旋转框——四顶点预旋转位置有框线色、轴对齐包围盒四角外侧无框线色（证明不是水平框）。
3. `TestVisualPolygon`：三角形——三条边采样点在多边形填充色内、顶点手柄存在。
4. `TestVisualChips`：两条不同类别标注开 chip——两处 chip 背景色互不相同且等于各自类别色；`toggleLabels` 后同位置不再是 chip 背景色。
5. `TestVisualMarquee`：框选手势中途—— marquee 矩形边框/填充色出现在拖拽区域。
6. `TestVisualEmptyState`：无图片打开时的初始帧不 panic、状态栏区域为背景色。

颜色断言用主题色/类别色的 RGB 容差比较（±2），不写金色文件。

## §5 导出可取消

### 5.1 exporter（W3a）

- `WriteExport` 签名改为：

  ```go
  func WriteExport(ctx context.Context, outDir, imgRoot string, plan *Plan, progress func(done, total int)) error
  ```

  - 每个循环迭代（每张图片拷贝、每个文件写入）开头检查 `ctx.Err()`；`nil` ctx 视为 `context.Background()`（防御，不 panic）。
  - 取消时走与错误相同的 `cleanup`（删 tmp 目录），返回 `fmt.Errorf("exporter: %w", ctx.Err())`，使 `errors.Is(err, context.Canceled)` 成立。
  - 拷贝单个大文件中途不响应取消（每文件粒度足够，PoC 接受，写进注释）。
- 既有调用点（app 的 `exportStart` 无窗口内联路径、exporter 全部测试）机械适配，保持编译绿。

### 5.2 app 向导（W3b）

- `exportState` 增加 `Cancel context.CancelFunc`。
- `exportStart`：`ctx, cancel := context.WithCancel(context.Background())` 存入 `s.Cancel`，goroutine 调 `exporter.WriteExport(ctx, ...)`。
- 输出页 Busy 时显示「取消导出」按钮（`ui.Button(c.Key("expCancel"), ...)`），点击调 `s.Cancel()`；取消中按钮 `Disabled(true)` 防连击。
- `finishExport`：`errors.Is(err, context.Canceled)` 时 `s.RunErr = "已取消导出（未写出任何文件）"`，`a.toast = "导出已取消"`；不算 Done，可修正后重新开始（tmp 已被 exporter 清理，重跑无碍）。
- 关闭守卫变化：`closeExportWizard` 在 Busy 时现在先 `s.Cancel()` 再隐藏（不再放任后台僵尸导出）；`finishExport` 及 progress 回调的 `a.ex.Plan != plan` 陈旧守卫保持，向导已关闭/重开时不写状态。
- `Done` 或取消后 `s.Cancel` 置 nil。

## §6 VOC seg PNG 掩码（W3a）

- `Options` 增加 `VOCSeg bool`（json `voc_seg,omitempty`）。默认 false = 维持现状（跳过 + `RuleSegDropped`）。**向导（W3b）里该复选框默认勾选**，即用户层面默认导出掩码。
- `VOCSeg = true` 时多边形不再丢弃：
  - XML object：与 det 相同结构，`bndbox` = 多边形外接框取整（`VOCOneBased` 同样生效），越界裁剪计 `RuleClipped`。
  - 栅格化（新文件 `internal/exporter/raster.go`）：
    - 多环先过既有 `bridgeRings` 桥成单环（计 `RuleMultiRing`），再 scanline even-odd 填充到 `*image.Paletted`。
    - Class PNG：像素值 = 类别在 `planCategories` 结果中的 0-based 序号 **+1**（0 留给背景）。掩码类别数上限 254：序号 +1 > 254 的类别，其 seg 标注丢弃并按 `RuleDropped` 计数（报告里可见，PoC 接受）。
    - Object PNG：同一图内 seg 对象按标注顺序编号 1..N；N > 255 时超出的 seg 对象丢弃并 `rec.warn` 一次。
    - 同图多对象按 z 序（标注顺序）依次写入，**后写覆盖先写**（VOC 语义）。
    - 顶点越界：填充裁剪到 `[0,w)×[0,h)`，发生过裁剪计 `RuleClipped`；栅格化为零像素的 seg 丢弃，计 `RuleDropped`（message "多边形栅格化为空"）。
    - 调色板：VOC 官方 22 色（0=黑背景，1..21），22..254 循环复用 1..21 的颜色，255 白（border 约定，不用但占位）；`image/png` 编码。
  - 文件：仅对**至少有 1 条保留 seg 对象**的图片写 `SegmentationClass/<stem>.png` 与 `SegmentationObject/<stem>.png`（无 seg 的图片不写空掩码，VOC 惯例允许）。
  - `RuleSegDropped` 仅在 `VOCSeg=false` 时使用；警告文案相应区分。
- 测试：小尺寸（如 8×6）确定性多边形像素级断言、z 序覆盖、多环桥接、超界裁剪、上限降级、调色板字节、与 XML bndbox 的一致性。

## §7 split 手动逐图调整

### 7.1 exporter（W3a）

- `SplitPolicy` 增加字段：

  ```go
  // Overrides forces individual images (keyed by Image.File) into "train" or
  // "val" after Mode assignment. Unknown keys and other values are an error.
  Overrides map[string]string `json:"overrides,omitempty"`
  ```

- `assignSplits` 末尾应用 Overrides：值必须 ∈ {`train`,`val`}（否则报错指明键与值）；键必须存在于图片集（否则报错指明键）。Overrides 与 SplitRandom 组合合法（先随机再强制个别图片）。
- 校验失败经 `BuildPlan` 返回 error（不落盘，符合两阶段语义）。

### 7.2 app 向导（W3b）

- `exportState` 增加 `Overrides map[string]string` 与 `Adjust bool`（调整子对话框开关）、`AdjustFilter string`。
- `options()` 把 Overrides 挂进 `SplitPolicy`（nil/空 map 时不挂）。
- 报告页（第 3 步）新增「逐图调整…」按钮打开子对话框（`ui.Modal` 叠在向导 Modal 上，参考既有 catPick/zMenu 的 Popover/Modal 用法；注意同一父元素 Key 唯一）：
  - 顶部筛选 `TextInput`（按文件名子串过滤，大小写不敏感）。
  - 虚拟化列表（`ui.List` 或 `ui.GridView`，参考 thumbs 的回收注意事项：行状态不放行元素）展示过滤后的图片：行 = 文件名 + 当前 split 标签；点击行在 train/val 间切换，写 `s.Overrides[file]`（与自动结果一致时从 map 删除该键，保持 Overrides 最小）。
  - 底部：「全部重置」（清空 Overrides）、「完成」（关闭）。
  - 关闭时若 Overrides 有变化 → 重新 `a.exportBuildPlan()`（纯计算，万级图片也在主线程可接受；若实测 >200ms 再改后台），报告页计数随即刷新。
- Overrides 变化后第 4 步的 Plan 已是新的，不需要额外失效逻辑；但 Busy/Done 状态下禁止打开调整对话框（按钮 Disabled）。

## §8 i18n 消息目录（W4，最后一波）

- `mygo.json` 增加：`"i18n": { "dir": "locales", "source": "zh-Hans" }`。
- 新建 `locales/zh-Hans.json`（源语言，含全部键与 `@<key>` 描述/placeholders）与 `locales/en.json`（完整翻译）。键扁平 camelCase、按前缀分组：`toolbar*`、`status*`、`hint*`、`catPanel*`、`props*`、`menuZ*`、`catPick*`、`wizardImport*`、`wizardExport*`、`toast*`、`closeGuard*`。计数用 `{n, plural, ...}`（中文只有 other 分支也要写）。
- 跑 `go tool mygo generate` 生成 `locales/catalog_gen.go` 并**提交**（`go build`/`go test` 不依赖 CLI）。生成代码会自动按随附语言链接 MyGo 自身字符串的 zh-Hans/en 翻译。
- `internal/app` 全部用户可见字面量改为 `locales.Xxx(...)`（含 `ui.Textf` 改为带占位符的 catalog 函数 + `ui.Text`）。`main.go` 窗口标题 "MyLabel" 是品牌名，保持字面量。
- **范围外（明确不做）**：store/importer/exporter/ir/imaging 的 error 字符串保持中文（经 err.Error() 进 UI，记为已知限制）；docs 不翻译。
- 测试：`internal/app` 增加 `TestMain(m *testing.M)`，`os.Setenv("MYGO_LOCALE", "zh-Hans")` 后 `m.Run()`——保证非中文 locale 机器上断言中文的既有测试仍绿。新增冒烟测试：`locales.In("en")` 的几个代表键非空且不等于 zh-Hans 版本。
- 门禁追加：`go tool mygo i18n check` 零 error（warning 只允许"翻译省略的参数"之外也无，目标零 warning）。
- AGENTS.md 常用命令追加 `go tool mygo i18n check` 与「改 UI 文案 = 改 locales/*.json + regenerate」。

## §9 性能回归（W5，主代理执行）

- `go run ./cmd/genimg -dir ./testdata/imgs10k -n 10000`：打开目录，记录扫描入库耗时、缩略图条滚动主观流畅度、首图加载耗时。
- `go run ./cmd/genimg -dir ./testdata/imgsbig -n 6 -big`：打开 >8000px 大图，验证分块加载、缩放/平移无错位。
- 复跑 `go test -bench=Benchmark -run='^$' ./internal/app/`，与阶段 0 基线对比（噪声大，看趋势）。
- 结果记录进 `开发计划书.md` 阶段 5 验收记录。

## 非目标（本阶段明确不做）

- 三平台打包（macOS dmg、Linux deb/rpm）、WASM web 版。
- 应用菜单栏（App.SetMenu）、自由取色器、属性筛选。
- 后端包错误字符串的 i18n。
