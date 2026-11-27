# 阶段 7 设计契约：SAM2 智能标注辅助

> 状态：设计定稿，待实现。前置依赖：`kisaragychihaya/libsam2_ncnn` 的
> `sam2_ncnn.dll`（C ABI）与 `sam2-small-encoder_fixed.ncnn.param` /
> `sam2-small-decoder_fixed.ncnn.param` 模型文件。

## 1. 目标与非目标

目标：在 MyLabel 中加入「AI 点选」工具，用户在画布上点若干前景点、双击后
调用 SAM2（ncnn 推理）生成标注，免手动画轮廓。

- 输出形态：多边形（轮廓抽稀后）或轴对齐矩形（mask 外接矩形），由工具内模式切换。
- 生成结果是普通标注：走 `cmdCreate`，一条 undo，参与保存/导出/所有既有流程。

非目标（本阶段不做）：

- 负点提示、框提示、多点交互式精炼（模型支持，UI 不做）。
- mask 叠加预览、模型管理对话框。
- 非 Windows 平台运行（DLL 不存在时工具无法激活，见 §5）。
- 自动保存模型路径设置（只认 §4 的探测规则）。

## 2. 交互契约（docs/phase6 风格的单激活工具模型）

- 工具条在创建工具组（分隔线之后、矩形/多边形/旋转框）末尾常驻第 8 个按钮
  「智能标注」，魔棒图标，快捷键 **S**——任何机器上都在（无运行时仅不可激活）。
- 运行时探测（§4）失败时点按钮/按 S：不切换工具，改为弹出「下载智能标注模型」
  同意框（说明将下载 SAM2.1-small 约 106MB、可取消）；同意后进入进度弹窗（当前
  文件 + 百分比 + n/5 文件数），Esc、点遮罩或「取消」都会中断下载（`.tmp` 原子
  改名保证不留半成品，重下自动续传已存在的文件）。下载完成客户端创建后，再按
  按钮才走正常激活流程。启动时若连 DLL 也缺，后台静默补 DLL（用户无感知，模型
  下载流程内部也会幂等兜底）；测试用 `MYLABEL_NO_SAM_BOOTSTRAP=1` 禁用。
- 无运行时按钮的 Tooltip 附注「首次使用需下载模型」。
- 按钮点击行为与缩放工具同构（`chooseTool`）：未激活 → 激活；已激活 →
  切换输出模式（多边形 ⇄ 矩形），图标随模式变化。
- 激活后单击画布：追加一个前景点（图像像素坐标，十字标记画在画布上）。
- 收集到**第一个**点时即在后台预编码当前图（§6「预编码」）：双击时通常只剩
  解码。预编码是尽力而为的动作——状态栏会告知开始与结果（「正在后台编码图
  像…」/「后台编码完成」/「后台编码失败，双击时会重新编码」），不占 `aiBusy`，
  失败不打扰操作（下次首点重试），全局同时只允许一个在途（别的图触发直接跳
  过），embedding 仍只缓存当前图。
- 双击：以已收集的点为 prompt 运行推理，生成标注（多边形模式）或外接矩形
  （矩形模式），随后清空已收集的点。双击的第二击不新增点，只把最后一个点
  更新到指针位置再触发。embedding 尚未就绪时（编码还在跑或已失败）由这次
  推理自己编码一次。
- Esc：清空已收集的点（忙碌中也丢弃结果，见 §6）。
- 切工具/切图/关工作区：丢弃已收集的点；切图与工作区时释放缓存的 embedding。
- 无类别表时与三个创建工具一样被拦截，提示「请先在右侧面板添加类别」。
- 工具属于创建工具：`isCreationTool` 返回 true（切入清空选择、按在任何
  标注上都直接取点、不选中/不编辑已有标注）。

## 3. 推理流水线

1. 双击触发。首个点首次推理时需要 encoder embedding：解码当前图片文件为
   RGB24（复用 `image/jpeg|png` 解码 + EXIF 已由扫描期归一的事实，直接从
   `img.Path` 解码），调用 `sam2.Encode`。embedding 按图片缓存（见 §6）。
   收集到第一个点时就已在后台预编码（§6），所以双击时通常只剩第 2 步；
   缓存里没有时由这次推理自己编码。
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
2. exe 同级完整运行时：`sam2_ncnn.dll` + `models/sam2-small-encoder_fixed.ncnn.param` +
   `models/sam2-small-decoder_fixed.ncnn.param` 及各自 `.bin` + `sam2_prompt.bin`
   （DLL 要求它在 decoder .param 同级，`load_prompt_consts`）。

缺任一文件 → 探测失败 → 工具不出现。DLL 的 Vulkan 版可直接使用：无 GPU 的
机器按设计回退 Cpu 并告警；teardown 由 `sam2_shutdown` 显式销毁（含 Vulkan
设备），Client 的释放顺序（embedding → 两个 net → shutdown）已在
`internal/sam2` 和 App 侧固定。探测只做文件存在性检查（不加载 DLL），
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
- `aiEncBusy bool`、`aiEncFor int` — 后台预编码是否在途及其所属图片下标（-1=无）。
- `aiEncodeHook func(info *ImageInfo, imgIndex int) (sam2.Embedding, error)` —
  测试注入的编码器；nil 时走真实路径（`readRGB24` + `sam.Encode`）。

预编码（encode-ahead）：`aiClick` 追加点后若只收集到 1 个点，调用
`aiEncodeAhead()`。前置条件：当前图存在、缓存未持有该图的 embedding、
没有预编码在途（单个 `aiEncBusy` 同时覆盖「同图已在编码」与「他图在途」
两种情况）、没有推理在途、编码器可用（hook 或 `sam` 至少一个非 nil）。它
`aiGen++` 取自己的代号、置 `aiEncBusy/aiEncFor`，把图像快照与编码器快照带
出去后内联执行（`win == nil`）或起 goroutine 经 `win.Update` 调
`aiEncodeLanded`。落地先清在途标志：出错把状态栏改为「后台编码失败」（双击
会再编码并报告那时的错误）；代号或图片已变得（Esc/切图/新的推理）则 `Release`
丢弃、状态栏保持现状；成功收编后状态栏改为「后台编码完成」。

`aiAdoptEmbedding` 是缓存唯一的写入口（`aiFinish` 编码成功的分支也走它）：
释放原住民、写入新 embedding。它依赖的不变式是「被替换的 handle 没有被任何
在途推理使用」：`aiEncodeAhead` 在 `aiBusy` 期间不启动，因此落地时代号仍有
效 ⟺ 期间没有触发、Esc 或切图；`aiFinish` 只在自己的 worker 返回后运行，而
编码成功的分支（fresh）本来就没用缓存解码。预编码与推理共用 `aiGen`，只有仍
有效的那次能通过代号比对，因此两次收养不会交错；两边都为同一张图编码时只浪费
一次 Encode——每个 handle 只有一个所有者，且 native 层对 handle 加锁
（`sam2_ncnn.cpp` 的 `enc->mu` / decoder 的锁），并发编码安全，只是串行。

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
  - 预编码：首点触发（hook 计数、参数是当前图、无窗口内联落地、Esc 后命中
    缓存不再编码）、去重（同图在途/他图在途/已缓存/空类别表都不触发）、
    过期丢弃（代号变、换图、失败三种落地都不收编且清在途标志）、
    与推理配合（预编码成功时状态栏为「解码中」，无缓存时为「编码中」，
    两种情况都正常出标注）。
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
