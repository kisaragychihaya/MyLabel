# 阶段 6 设计契约：左侧工具栏（Photoshop 式）

绑定文档。用户已拍板：六个工具**全部进左栏**（抓手/缩放/选框 + 矩形/多边形/旋转框），**SVG 图标 + Tooltip**；后续 SAM 智能分割/选点工具也将加在这条栏上（本期不做，架构上预留——工具条与工具枚举必须易于追加）。

## §1 工具模型

- `Tool` 枚举追加三个值：`ToolPan`（抓手）、`ToolZoom`（缩放）、`ToolMarquee`（选框），与既有 `ToolRect/ToolPoly/ToolObb` 同属单一激活工具态 `a.tool`（radio 语义，同一时刻只有一个）。默认工具维持 `ToolRect` 不变。
- 快捷键（`keyDown`，沿用 `ev.Mods == 0` 守卫，与 A/D/F 一致）：
  - `H` = ToolPan、`Z` = ToolZoom、`M` = ToolMarquee；既有 `R/P/O/L` 不变。
  - 注意纯 `Z` 与 `Ctrl+Z` 的区分靠修饰键守卫，既有测试不得回归。
- 工具切换时：进行中的草稿（多边形/obb）按 Esc 语义取消（`cancelDrag` 已有的草稿分支），手势中不切换（`dragInProgress()` 时快捷键/按钮均忽略）。`setTool` 现有逻辑可复用，需要时扩展。

## §2 左工具条布局（app.go / 新文件 toolstrip.go）

- 主装配变为：`工具条 | 画布 | 类别面板`（`ui.Row` 内 `a.toolStrip(c)` 插到 `a.canvasView(c)` 之前），底部缩略图条与状态栏不动。
- 工具条：竖向 `ui.Column`，固定宽（≈44 DIP），背景 `t.Surface`，与画布间一条分隔线；按顺序六个图标按钮，视图类（抓手/缩放/选框）与创建类（矩形/多边形/旋转框）之间一条 `ui.Divider`。
- 按钮：正方形图标按钮，激活工具用 `ui.PrimaryButton`、其余 `ui.Button`（与既有 toolButton 的高亮模式一致）；每个按钮带 Tooltip「名称 + 快捷键」（如 `抓手 (H)`）。Tooltip 用法查 `docs-zh/ui/tooltip.md`，不要猜 API。
- 顶部工具栏：移除「工具:」标签与 矩形(R)/多边形(P)/旋转框(O) 三个按钮；「标签 (L)」保留在顶部；提示文字更新（删工具切换提示，保留其余）。
- 图标（新文件 `internal/app/icons.go`）：六个**手写原创** 24×24 stroke SVG（`stroke="currentColor"`、fill none、stroke-width 2、圆角线帽），`//go:embed` 或内联字符串 + `ui.MustParseSVG`。语义：抓手=手掌、缩放=放大镜（带 +）、选框=虚线方框、矩形=方框、多边形=五边形、旋转框=旋转的方框。风格统一、视觉重心居中即可，不追求完美。

## §3 各工具的交互语义（interact.go）

既有习惯**全部保留**：Space+左键平移、Shift+空白拖拽框选、Ctrl+滚轮缩放、中键拖拽平移，在任何工具下都可用。

- **ToolPan（抓手）**：左键在画布任意处按下即平移（复用 spacePan 的平移数学）；不选中、不创建、不移动标注。光标 `ui.CursorGrab`（canvas.go 现有 spacePan 光标分支扩展：`spacePan || tool==ToolPan`）。
- **ToolZoom（缩放）**：左键单击 = 以点击点为锚放大（`zoomAround`，factor 1.25）；Alt+单击 = 缩小（factor 0.8）。单击不选中标注。拖拽本阶段不定义 scrub（按下即移动时视为误触，什么都不做——pointer 位移超过点击阈值则本次不缩放）。锚点是点击处图像点（`zoomAroundPoint`，不要锚图片中心）。
- **ToolMarquee（选框）**：左键按下即进入 marquee（复用 Shift+拖拽的既有路径，即使起点落在标注上）；拖出矩形松手后选中相交标注（既有语义）；原位单击（未拖出阈值）= 清空选择。
- **chip 与右键菜单的跨工具行为（明确决策）**：chip 命中优先级规则不变（单选/双击类别列表在**所有**工具下可用——chip 是独立控件）；右键 z 序菜单在**所有**工具下可用（右键不与三视图工具的左键手势冲突）。
- 数字键改类别、Ctrl+A/Delete/Esc 等在所有工具下维持既有语义。

## §4 i18n（硬性要求）

项目已接消息目录：所有新文案（六个工具名 Tooltip、缩放状态提示等）必须加进 `locales/zh-Hans.json` 与 `locales/en.json`（带 `@key` 描述），然后 `go tool mygo generate` 重新生成 `catalog_gen.go`，代码里用 `locales.Xxx()`。工具条图标按钮**必须**有可访问名称（`Label`，docs-zh/ui/icon.md 辅助功能一节）。门禁含 `go tool mygo i18n check` 零 error。

## §5 测试

- 工具快捷键 H/Z/M + 既有 R/P/O/L 回归；纯 Z 不影响 Ctrl+Z。
- ToolPan：拖拽改变 panX/panY，不产生选择/草稿；光标分支单测。
- ToolZoom：单击放大且点击处图像点不动（`zoomAroundPoint` 不变式断言）；Alt+单击缩小；微小位移的误拖拽不触发缩放。
- ToolMarquee：拖拽选中相交标注（与 Shift+拖拽同结果）；原位单击清空选择；起点在标注上也进 marquee。
- 跨工具：ToolPan 下点 chip 仍选中该标注、双击 chip 仍弹类别列表、右键仍弹 z 序菜单。
- 工具条 UI smoke：六个按钮渲染、激活高亮、切换工具取消进行中草稿。
- 遵守测试约定（一 Tester/函数、`tt.Advance`、合成事件走 `handleCanvasInput`、文案断言查 `locales.*`）。

## 非目标

- SAM 智能分割/选点工具、缩放工具的 scrubby-drag 与框选放大（drag-zoom）、工具条自定义（拖拽排序/折叠）。
- 右键菜单/属性面板/类别面板的布局调整。
