# 阶段 4 设计契约：导出器与项目文件夹生成

绑定文档：`开发计划书.md` §5（格式转换规范）与 §6 阶段 4 清单。本文件是
`internal/exporter` 的**唯一权威契约**，实现不得偏离；有疑义先改本文件。

## 1. 范围与边界

- 输入：IR（`*ir.Project` + `[]ir.Image`，图像绝对像素坐标）+ 图片根目录。
  exporter **不读 store、不认识 app 模型**；app 侧负责用 `irconv` 已有的
  `annToIR`/`appCategoriesToIR` 装配 IR（W2 的事）。
- 输出：一个**新目录**（已存在则报错，不合并、不覆盖）：临时目录组装 →
  复制图片 → 写标注/清单 → 原子改名。
- 两阶段：**BuildPlan**（纯计算，dry-run 报告的数据源，不碰磁盘）→
  **WriteExport**（执行 Plan 落盘）。UI 在确认前只跑 BuildPlan。
- 不做（本阶段明确排除）：
  - VOC seg 的 PNG 掩码栅格化（seg 标注在 VOC 导出中跳过并计数）。
  - split 的手动逐图调整（preserve / 随机比例两种，手动调整留待阶段 5）。
  - 原始标注文件留存回写（§5.4 工程保险 1，导入侧未实现 `imports/`，暂缓）。
  - 导出取消（WriteExport 同步执行，UI 放 goroutine；不可中途取消）。

## 2. 公共 API（internal/exporter/exporter.go，契约层已实现）

```go
type Format string  // FormatYOLO | FormatCOCO | FormatVOC
type Task string    // TaskDet | TaskSeg | TaskObb（仅 YOLO 必填）
type SplitMode string // SplitPreserve | SplitRandom

type SplitPolicy struct { Mode SplitMode; TrainRatio float64; Seed int64 }

type Options struct {
    Format          Format
    Task            Task        // 仅 YOLO；COCO/VOC 忽略
    Split           SplitPolicy // 零值 = preserve
    VOCOneBased     bool        // VOC：坐标 1-based（VOC 官方惯例）；false = 0-based
    VOCDowngradeOBB bool        // VOC：rotated 写 bndbox 而非 robndbox（有损，用户显式选择）
}

type Issue struct { File, AnnID, Rule, Message string }
type LossyStat struct { Rule string; Count int; Samples []Issue } // Samples ≤5
type Report struct {
    Images, ImagesWithAnns, Annotations, AnnotationsDropped int
    Lossy    []LossyStat
    Warnings []string
}

type OutFile struct { Rel string; Data []byte }     // Rel：导出根内 slash 相对路径
type ImageCopy struct { Src, Dst string }           // Src 相对图片根，Dst 相对导出根

type Plan struct {
    Opts       Options
    Splits     map[string]string  // image.File → "train"|"val"
    Categories []ir.Category      // 已按目标格式赋好 Index/COCOID
    Files      []OutFile
    Copies     []ImageCopy
    Report     Report
}

func BuildPlan(proj *ir.Project, images []ir.Image, opts Options) (*Plan, error)
func WriteExport(outDir, imgRoot string, plan *Plan, progress func(done, total int)) error
```

有损规则常量（Report.Lossy 的 Rule 取值，UI 与 manifest 共用）：
`RuleObbToDet / RuleSegToDet / RuleObbToSeg / RuleDetToSeg / RuleDetToObb /
RuleSegToObb / RuleMultiRing / RuleObbToCOCO / RuleObbToBndbox /
RuleSegDropped / RuleClipped / RuleDropped`。

### 2.1 BuildPlan 职责（契约层实现）

1. 校验：proj 非 nil；Format 合法；YOLO 必须带合法 Task；SplitRandom 时
   `0 < TrainRatio < 1`；image.File 非空且不重复。
2. `assignSplits(images, opts.Split)`（split.go）得 file→train/val 映射。
3. 按 Format 分派 `buildYOLOPlan / buildCOCOPlan / buildVOCPlan`
   （签名：`func buildXxxPlan(proj *ir.Project, images []ir.Image, splits map[string]string, opts Options) (*Plan, error)`，各自文件实现）。
4. 统一后检：`Files[].Rel` 与 `Copies[].Dst` 全局无重复（**大小写不敏感**
   比较，Windows 文件系统）；有重复 → error 列出冲突。
5. 填 `Plan.Opts` 返回。Plan 不含 manifest 文件——manifest 由 WriteExport
   从 Plan 现生成（保证 dry-run 与落盘一致）。

### 2.2 WriteExport 语义（契约层实现）

- `outDir` 已存在 → 报错（不覆盖）；先写 `outDir + ".mylabel-tmp"`，全部
  成功后 `os.Rename`；任何错误清理 tmp。
- 图片复制：流式 `io.Copy`（1MB 缓冲），逐张 `Src(imgRoot 相对) → Dst`。
- 所有 Rel/Dst/Src 路径拒绝绝对路径与 `..` 逃逸（同 ir.cleanRelPath 规则）。
- progress 回调总计 `len(Copies)+len(Files)+1`（+1 是 manifest）；可为 nil。
- 末尾写 `mylabel-export.json`（manifest）：tool/ir_version/format/task/
  options/split/categories/坐标基准（0-based 像素，越界已裁剪）/Report 全文/
  非标准声明（在 Report.Warnings 里，如 COCO obb 私有扩展）。

## 3. split 划分（split.go，Agent A）

`func assignSplits(images []ir.Image, pol SplitPolicy) (map[string]string, error)`

- `SplitPreserve`（零值默认）：File 剥掉一层可选的 `images/` 前缀后，第一段
  路径是 `train`/`val`（不区分大小写）则沿用，否则归 `train`。Ultralytics
  源布局（images/train/x.jpg）因此能识别 split。
- **目标路径去重段**（`exportRel`，QC W1 追加）：所有格式计算 Dst 时先剥
  `images/` 前缀、再剥一层 `train/val/test` 段——源是 Ultralytics 布局时
  导出不会叠出 `images/train/train/x.jpg`；`ImageCopy.Src` 永远用原始路径。
  剥段后撞名由 BuildPlan 第 4 步查重报错。
- `SplitRandom`：以 Seed 对**排序后的 File 列表**做确定性洗牌（
  `math/rand/v2` `rand.New(rand.NewPCG(seed, seed^0x9E3779B9))`），前
  `round(n*TrainRatio)` 张为 train（至少 1 张，n≥2 时 val 也至少 1 张），
  其余 val。同输入同 seed 结果必须一致（有测试）。

## 4. 有损几何换算（convert.go，Agent A）

全部纯函数，输入不改，越界裁剪计入 RuleClipped。签名已在 convert.go
声明（stub），语义：

- `aabbOfPoints / aabbOfRings`：外接轴对齐矩形（x,y,w,h）。
- `convexHull`：单调链凸包（共线点剔除）。
- `minAreaRect(pts) (cx, cy, w, h, angle)`：旋转卡壳最小外接旋转矩形；
  angle 为 w 边方向（弧度，图像坐标 y 向下），与 ir 的 quadParams 约定一致。
- `bridgeRings(rings) [][2]float64`：多环并单环（Ultralytics
  merge_multi_segment 做法：按外环面积降序，环间用最短距细桥连接成自交
  单环）。单环原样返回。
- `clipBBox / clipPoints`：裁到 [0,W]×[0,H]，报告是否动过；bbox 裁完
  w/h ≤ 0 → 调用方按 RuleDropped 丢弃。

浮点输出格式化（yolo.go 用）：归一化值保留 6 位小数并去尾零
（`strconv.FormatFloat(round6(v), 'f', -1, 64)`）。

## 5. YOLO 导出（yolo.go，Agent A）

布局（split 映射到 train/val 子目录，镜像源相对路径）：

```
<out>/images/<split>/<原相对路径>
<out>/labels/<split>/<原相对路径，扩展名换 .txt>
<out>/data.yaml
```

- txt：一行一对象，空格分隔，类别 0-based（= Categories 顺序），坐标
  归一化到 [0,1]（除以图宽/高，先裁剪）。**无标注图片不写 txt**，图片照拷。
- det（5 字段 `class cx cy w h`）：bbox 直写；rotated → 外接框
  （RuleObbToDet）；polygon → 外接框（RuleSegToDet）。
- seg（`class x1 y1 ...`，≥3 点）：bbox → 矩形四点（RuleDetToSeg）；
  rotated → 四点（RuleObbToSeg）；polygon 单环直写，多环 → bridgeRings
  （RuleMultiRing）。
- obb（9 字段四点）：bbox → 矩形四点（RuleDetToObb）；rotated → Points
  直写（真源）；polygon → minAreaRect 四点（RuleSegToObb，高有损警告）。
  四点顺序用 ir 规范化序（orderQuad 约定：绕质心逆时针、最左顶点起）。
- 未知类别 / 几何全裁没 → RuleDropped 跳过并计数。
- `data.yaml` **手写拼接**（禁止引入 yaml 库）：
  ```yaml
  train: images/train
  val: images/val        # 无 val 图片时省略此行
  names:
    - cat1
    - cat2
  ```
  **不写 `path:`**（QC W1 修正）：BuildPlan 拿不到 outDir，而 Ultralytics
  在 path 缺省时回退到 yaml 自身所在目录——正好是导出根，比写错路径稳。
  names 用列表形式（天然保序，替代 §5.1 的 mapping 写法）；name 做单行
  YAML 安全转义（含 `: # ' "` 等特殊字符或前后空白、布尔/数字样字面量时
  单引号包裹、内嵌 `'` 双写）。
- 标注属性（Attributes）无对应字段：Report.Warnings 加一条汇总
  「N 条标注的属性不随 YOLO 导出」。

## 6. COCO 导出（coco.go，Agent B）

布局：

```
<out>/images/<split>/<原相对路径>
<out>/annotations/instances_<split>.json   # 每个非空 split 一个文件
```

- JSON 用 `encoding/json` 紧凑输出（不 indent）。`info` 写
  `{description, version: ir.Version, date_generated 省略}`；`categories`
  id = IR COCOID（有则沿用）否则 1-based 顺位；`images` id 从 1 递增
  （按 File 排序序）；`annotations` id 全文件从 1 递增。
- bbox：`[x, y, w, h]` 绝对像素（左上角原点），先裁剪；area = w*h。
- polygon → `segmentation: [ring0, ring1, ...]`（多环原生支持，扁平化
  `[x1,y1,...]`）+ 外接 bbox，iscrowd=0。
- bbox → bbox + `segmentation: [[矩形四点]]`（合法弱语义，记 RuleDetToSeg）。
- rotated → 四点 `segmentation` + 外接 bbox + **私有扩展字段**：
  `"mylabel_obb": {"points": [[x,y]×4], "angle": 弧度}`（RuleObbToCOCO），
  Warnings 声明「mylabel_obb / mylabel_attributes 为非标准扩展」。
- Attributes → `"mylabel_attributes": {...}`（omitempty）。
- 空 split（无图片）不生成对应 json。无标注图片有 images 条目、无
  annotations 条目。

## 7. VOC 导出（voc.go，Agent A）

布局（VOC 经典平铺，id = 去扩展名 basename）：

```
<out>/JPEGImages/<basename>.<ext>
<out>/Annotations/<basename>.xml
<out>/ImageSets/Main/train.txt / val.txt   # 每行一个 id
```

- **basename（去扩展名，大小写不敏感）必须全局唯一**，冲突在 BuildPlan
  第 4 步统一检出报错（voc.go 只需保证 Dst 用 basename）。
- XML（encoding/xml，头部 `<?xml version="1.0" encoding="UTF-8"?>`）：
  `<folder>JPEGImages</folder>` `<filename>` basename 含扩展名 `<size>`
  （width/height/depth=3）。`<object>`：`<name>` 类别名 + `<bndbox>`
  （xmin/ymin/xmax/ymax，像素取整 round，1-based 时 +1 由 VOCOneBased
  控制）。无标注图片写无 object 的 xml。
- rotated 默认写 `<robndbox><cx><cy><w><h><angle>`（roLabelImg 私有扩展，
  弧度制；angle = w 边方向、图像坐标 y 向下，即 IR derived cache 原样）；
  `VOCDowngradeOBB=true` 时降级外接 bndbox（RuleObbToBndbox）。
- polygon：**不支持**，整条跳过（RuleSegDropped）并在 Warnings 汇总
  「VOC 导出不含分割，N 条 seg 标注被跳过」。
- 未知类别 → RuleDropped。

## 8. 测试要求（W1 门禁的一部分）

- convert：凸包/最小外接矩形已知解用例（正方形、旋转 45° 矩形、共线点）、
  bridgeRings 单环恒等/双环连通、clip 边界。
- split：preserve 路径识别、random 同 seed 复现、ratio 边界（至少各 1）。
- yolo：det/seg/obb 三任务各一个黄金文件用例（精确 txt 字节）、data.yaml
  保序与转义、归一化数值格式、无标注图片无 txt、多环桥接计数。
- coco：schema 关键字段（categories id 顺位、bbox 原点、segmentation 联合
  类型、mylabel_obb 扩展、iscrowd=0）、per-split 文件、空 split 省略。
- voc：bndbox 取整与 1-based 开关、robndbox 弧度原样、seg 跳过计数、
  xml 良好性（encoding/xml 能解析回）。
- 集成：BuildPlan(yolo)+WriteExport 到 TempDir，目录结构/txt 内容/manifest
  字段断言（Agent A）；coco 同（Agent B）。
- 数值断言容差 1e-6；所有测试禁依赖执行顺序。

## 9. W2（app 集成，另派）

- 工具栏「导出数据集」→ 向导 Modal（复用 importwizard 模式）：格式 →
  任务/选项（YOLO task 单选；VOC 两个复选）→ split 策略 → dry-run 报告
  （BuildPlan 结果，Lossy 逐条计数+示例）→ 选输出目录 → 后台
  WriteExport + 进度 + Toast。
- app→IR 装配：补一个 projectToIR（复用 annToIR/appCategoriesToIR），
  SHA256 取 store 的 hash。保存检查：有未保存修改先提示保存。
- 导出前未保存/无图片/无类别（有标注引用不到类别会 RuleDropped）的提示。
