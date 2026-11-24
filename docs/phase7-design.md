# 阶段 7 设计契约：SAM2 智能标注辅助

> 状态：设计定稿，待实现。前置依赖：`kisaragychihaya/libsam2_ncnn` 的
> `sam2_ncnn.dll`（C ABI）与 `sam2_encoder_fixed.ncnn.param` /
> `sam2_decoder_fixed.ncnn.param` 模型文件。

## 1. 目标与非目标

目标：在 MyLabel 中加入「AI 点选」工具，用户在画布上点若干前景点、双击后
调用 SAM2（ncnn 推理）生成标注，免手动画轮廓。

- 输出形态：多边形（轮廓抽稀后）或轴对齐矩形（mask 外接矩形），由工具内模式切换。
- 生成结果是普通标注：走 `cmdCreate`，一条 undo，参与保存/导出/所有既有流程。

非目标（本阶段不做）：

- 负点提示、框提示、多点交互式精炼（模型支持，UI 不做）。
- mask 叠加预览、模型管理对话框。
- 非 Windows 平台运行（DLL 不存在时工具自动隐藏，见 §5）。
- 自动保存模型路径设置（只认 §4 的探测规则）。

## 2. 交互契约（docs/phase6 风格的单激活工具模型）

- 工具条在创建工具组（分隔线之后、矩形/多边形/旋转框）末尾追加第 8 个按钮
  「智能标注」，魔棒图标，快捷键 **S**。仅当 §4 的运行时探测成功时显示；
  模型文件缺失时工具条保持原 7 按钮，功能零感知。
- 按钮点击行为与缩放工具同构（`chooseTool`）：未激活 → 激活；已激活 →
  切换输出模式（多边形 ⇄ 矩形），图标随模式变化。
- 激活后单击画布：追加一个前景点（图像像素坐标，十字标记画在画布上）。
- 双击：以已收集的点为 prompt 运行推理，生成标注（多边形模式）或外接矩形
  （矩形模式），随后清空已收集的点。双击的第二击不新增点，只把最后一个点
  更新到指针位置再触发。
- Esc：清空已收集的点（忙碌中也丢弃结果，见 §6）。
- 切工具/切图/关工作区：丢弃已收集的点；切图与工作区时释放缓存的 embedding。
- 无类别表时与三个创建工具一样被拦截，提示「请先在右侧面板添加类别」。
- 工具属于创建工具：`isCreationTool` 返回 true（切入清空选择、按在任何
  标注上都直接取点、不选中/不编辑已有标注）。

## 3. 推理流水线

1. 双击触发。首个点首次推理时需要 encoder embedding：解码当前图片文件为
   RGB24（复用 `image/jpeg|png` 解码 + EXIF 已由扫描期归一的事实，直接从
   `img.Path` 解码），调用 `sam2.Encode`。embedding 按图片缓存（见 §6）。
2. `sam2.Decode(emb, points, labels)`，points 为图像像素坐标 float32 对，
   labels 全 1。`Result` 含若干候选 mask 与 IoU，取 IoU 最大者。
3. mask 是 `maskH×maskW` 低分辨率 logits：取最大连通域（4 连通），
   多边形模式 `TraceContour` + `Simplify`（epsilon 1.5 mask 像素），顶点映射
   回图像坐标 `x_img = (x_mask+0.5) * imgW / maskW`；矩形模式取 BBox 同法映射。
4. 构建 `Ann`（`newPolyAnn` 或中心+宽高矩形），`cmdCreate` 入栈。

阈值：logit > 0（等价 sigmoid > 0.5），与 libsam2_ncnn QC 门禁一致。

## 4. 运行时探测（`internal/sam2.Detect`）

优先级从高到低：

1. 环境变量 `SAM2_DLL` / `SAM2_ENCODER` / `SAM2_DECODER`（可单独覆盖）。
2. exe 同级：`sam2_ncnn.dll` + `models/sam2_encoder_fixed.ncnn.param` +
   `models/sam2_decoder_fixed.ncnn.param`。

缺任一文件 → 探测失败 → 工具不出现。探测只做文件存在性检查（不加载 DLL），
首次推理才真正加载（`syscall.NewLazyDLL` 语义）。

## 5. 后端包

### 5.1 `internal/maskgeom`（纯函数，无依赖，可独立测试）

```go
type Pt struct{ X, Y int } // mask 网格像素坐标

// BBox 前景（v > thresh）的包含边界；无前景 ok=false。
func BBox(m []float32, w, h int, thresh float32) (minX, minY, maxX, maxY int, ok bool)

// LargestComponent 返回只保留最大 4 连通域的副本（无前景返回全零副本）。
func LargestComponent(m []float32, w, h int, thresh float32) []float32

// TraceContour Moore 邻域边界跟踪（Jacob 停止准则），返回有序外轮廓顶点，
// 首尾不重复；无前景返回 error。
func TraceContour(m []float32, w, h int, thresh float32) ([]Pt, error)

// Simplify Douglas-Peucker 抽稀，epsilon 单位为 mask 像素。
func Simplify(pts []Pt, epsilon float64) []Pt
```

### 5.2 `internal/sam2`（纯 Go FFI，CGO_ENABLED=0，syscall.NewLazyDLL）

参照 libsam2_ncnn `example/main.go` 的 ABI 绑定（decodeResult 布局、
toPointer/cstr、lastError），结构：

```go
type Config struct{ DLL, EncoderParam, DecoderParam string; DeviceID int }
func Detect(exeDir string) (Config, bool)

type Client struct { /* LazyDLL + LazyProc，惰性加载 */ }
func New(cfg Config) *Client
func (c *Client) Available() error                 // 加载 DLL 并校验全部导出
func (c *Client) Encode(rgb []byte, w, h, stride int) (Embedding, error)
func (e Embedding) Release()                       // sam2_embedding_destroy
func (c *Client) Decode(e Embedding, pts []float32, labels []int32) (Result, error)
func (c *Client) Close()                           // destroy 双 net + sam2_shutdown
type Result struct{ Masks [][]float32; IoU []float32; W, H int }
```

约束：`Decode` 与 `Encode` 不可并发调用（App 侧用 busy 标志串行化）；
`Close` 之后方法返回 error 而非 panic。`Available`/`Encode`/`Decode` 的错误
文本取自 `sam2_get_last_error`。

## 6. App 状态（internal/app）

新字段（App 结构体，注释说明生命周期）：

- `sam *sam2.Client` — `New()` 时 `Detect` 成功则创建，nil 表示功能不可用。
- `aiRectMode bool` — 输出模式（false=多边形）。
- `aiPoints []Point` — 已收集的前景点（图像像素）。
- `aiBusy bool` — 推理进行中；期间拒绝再次触发与切图不改结果。
- `aiGen uint64` — 代计数：Esc/切图/openDir/触发推理时 +1，
  异步结果回来时比对，不等即丢弃。
- `aiEmb sam2.Embedding`、`aiEmbFor int` — embedding 缓存及其所属图片下标。

异步纪律（铁律 2）：编码/解码在 goroutine 中执行，结果经 `win.Update`
回到主线程；`win == nil`（无窗口测试）时同步内联执行（参照
`exportStart` 的双路径）。图片在推理期间被切换 → 结果丢弃并立即
Release embedding。

embedding 释放在：切图、`openDir`、`Close`。

## 7. 画布与 i18n

- `canvas.go` 单 Draw 回调内追加 aiPoints 的十字标记绘制（≤ 点数级开销）。
- `icons.go` 增加魔棒图标两个变体（多边形模式：魔棒+星；矩形模式：魔棒+方框），
  随 `aiRectMode` 切换，同 `zoomToolIcon` 的模式。
- 文案键（zh-Hans.json 源 + en.json，带 description，改后
  `go tool mygo generate` 重新生成 catalog_gen.go）：
  - `toolStripAI`：名称/Tooltip（含 (S)），模式后缀由 Tooltip 函数拼接。
  - `statusAINeedCategories`（复用现有 `statusNoCategories` 即可，不新增）
  - `statusAIEncoding`、`statusAIDecoding`、`statusAIFailed(err)`、
    `statusAIDone(kind)`、`statusAINoMask`。

## 8. 测试计划

- `internal/maskgeom`：合成 mask 单测——实心矩形（BBox 精确、轮廓 4 顶点）、
  圆（抽稀后顶点数下降且都在圆上 ±1px）、L 形（凹点保留）、1 像素缺口
  （跟踪不断裂）、两个连通域（只保留最大）、全零（BBox ok=false、
  TraceContour 报错）、Douglas-Peucker 共线点抽成 2 点。
- `internal/sam2`：无 DLL 时 `Detect` 返回 false；有 DLL（CI/本机
  存在 `SAM2_DLL`）时跑通 encode+decode 冒烟并校验 mask 几何。
- `internal/app`：
  - 无 DLL 时工具条 7 按钮、`isCreationTool(ToolAI)`、S 键不 panic。
  - 有点收集/double-click 触发/Esc 清点的交互回归（注入假结果函数，
    不依赖真实 DLL）。
  - `buildAIAnn`（mask→Ann 的纯函数）用合成 mask 验证多边形/矩形两种模式。
  - 文案断言查 `locales.*` 表；`MYGO_LOCALE=en` 全绿。

## 9. 验收

```bash
go build ./... && go vet ./... && go tool mygo vet ./...
go tool mygo i18n check
go test ./... -count=1
MYGO_LOCALE=en go test ./... -count=1
```

本机端到端（有 DLL 时）：`sam2_ncnn.dll` + `models/` 放到 MyLabel exe 旁，
打开 `ncnn_run/qc/test_image.png` 所在文件夹，S 激活工具，点圆心区域，
双击 → 出现贴合圆的多边形标注；再次单击工具图标切矩形模式，重复 → 外接矩形。
