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
- **ToolZoom（缩放）**（2026-11-24 两次按用户反馈修订，最终语义）：**方向由工具条缩放图标（或 Z 键）切换**——ToolZoom 已激活时再点图标/再按 Z = 切换 zoomOutNext，图标 +/− 随动；未激活时点击 = 只选中工具。画布单击按当前方向以点击点为锚缩放（放大 ×1.25 / 缩小 ×0.8），不翻转状态；Alt+单击瞬时反向，不改状态。切入 ToolZoom 时方向重置为放大（setTool）。单击不选中标注；位移超阈值（dblClickSlopDIP）视为误拖不缩放。锚点是点击处图像点（`zoomAroundPoint`，不要锚图片中心）。切换入口统一为 `chooseTool`（手势进行中不响应）。
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

## §6 指针工具与创建/编辑分离（2026-11-24 用户反馈新增）

用户要求：创建工具（矩形/多边形/旋转框）激活时点击已有标注**不再选中它，而是直接开始新建**；选择/编辑收归独立的指针工具（Photoshop 移动工具，快捷键 `V`）。

### 6.1 工具模型

- 新增 `ToolSelect`（指针），快捷键 `V`（`ev.Mods == 0` 守卫，与其他工具键一致）。
- 工具条顺序变为：**指针 (V) / 抓手 (H) / 缩放 (Z) / 选框 (M)** ‖ 分隔线 ‖ 矩形 (R) / 多边形 (P) / 旋转框 (O)（分隔线索引 3→4，共 7 个按钮）。指针放最上（Photoshop 惯例）。
- **启动默认工具改为 ToolSelect**（Photoshop 惯例；避免打开图片后在已有标注上误开新框）。因此受影响的既有测试（默认 ToolRect 假设）由实现方逐一修正（显式 setTool）。
- 新图标：箭头指针 SVG（风格与既有六个一致）；toolSpecs 追加一行。

### 6.2 行为拆分

- **创建工具（Rect/Poly/Obb）= 纯创建**：pointerDown 落在任何位置（包括已有标注/手柄/边上）都直接进入创建手势（矩形橡皮筋 / 多边形·obb 加点），不再做标注命中选择、不再拖动/resize/旋转已有标注。空类别表的创建拦截与提示不变。
- **ToolSelect = 全部编辑交互**：单击选中、拖动移动、单选手柄 resize/旋转、多边形顶点编辑（拖/加点/删点）、Ctrl+点击多选、Shift+拖拽框选（仍全工具可用）、双击标注开属性面板、数字键改类别、Delete 删除。即"旧行为里落在标注上的那一半"。
- **切到创建工具时清空选择**（`setTool`/`chooseTool` 内）：选择态在创建工具下不可见也不可用（手柄不画）；切到 Select/Marquee 之间移动时保留选择。Delete/数字键在创建工具下因选择为空自然无效。
- **chip 与右键 z 序菜单维持全工具可用**（既有决策不变）：chip 是独立小目标，单击选中/双击类别列表在所有工具下有效；右键菜单同理。双击标注本体开属性面板改为**仅 ToolSelect**（chip 双击不受此限）。
- 光标：ToolSelect = 系统默认箭头；创建工具/选框 = crosshair；抓手 = grab；缩放维持现状（canvasCursor 扩展）。
- Space 平移、Ctrl+滚轮缩放、中键平移、A/D 切图、Ctrl+B 沿用等全局手势不变。

### 6.3 i18n

新增键 `toolStripSelect`（"指针 (V)" / "Select (V)"），zh-Hans/en 双写 + regenerate。

### 6.4 测试

- 创建工具下点击已有标注 = 开新草稿（mode/几何断言），选择集不变、无移动。
- ToolSelect 下：选中/移动/手柄/旋转/顶点编辑/双击属性面板全套回归（大量是既有用例迁移默认工具后的重跑）。
- V 快捷键与工具条第 7 按钮；默认工具 = ToolSelect；切到创建工具清空选择、切回 Select 后可重新选中。
- chip 单/双击、右键 z 菜单在创建工具下仍可用（跨工具轮询用例扩展为 7 工具）。
- 光标分支：ToolSelect = 箭头。
