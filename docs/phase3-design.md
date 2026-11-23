# MyLabel 阶段 3 设计文档 — IR 与导入器

状态：**契约**。前三阶段的铁律、坐标系、测试约定见 `AGENTS.md`、`docs/phase1-design.md`、`docs/phase2-design.md`。

## 1. 范围与分期

| 波次 | 内容 | 文件归属 |
|---|---|---|
| W1（三代理并行） | A: `internal/ir`（模型/校验/规范化/读写）；B: `internal/importer` 的 yolo+voc；C: `internal/importer` 的 coco | 各自新包/新文件 |
| W2 | IR↔app 转换 + 导入向导 UI（格式→映射→报告→应用）+ 直接打开 IR | `internal/app/` 全包 |
| W3 | 标注属性（store v4 + 属性编辑 Modal）+ obb 四点创建工具 | `internal/store`（v4）+ `internal/app/` |

**本阶段不做**：导出器（YOLO/COCO/VOC 写出）与项目文件夹生成——那是阶段 4；`split_policy`、RLE→多边形轮廓追踪（本阶段 RLE 降级 bbox + 属性标记，契约允许）。

## 2. internal/ir（W1 子代理 A）

IR 是**交换/互操作模型**，不取代 app 内部模型；两者经显式转换衔接（W2）。

### 2.1 模型（`internal/ir/ir.go`，主代理已落类型契约）

```go
const Version = "1.0"

type Kind string
const ( KindBBox Kind = "bbox"; KindPolygon Kind = "polygon"; KindRotated Kind = "rotated_bbox" )

type Annotation struct {
    ID         string            `json:"id"`
    Category   string            `json:"category"`        // 稳定字符串 key（非 index）
    Kind       Kind              `json:"kind"`
    X, Y, W, H float64           `json:"x,omitempty" ...` // bbox 专用，绝对像素，左上角原点
    Points     [][2]float64      `json:"points,omitempty"` // rotated_bbox 四点（真源）
    CX, CY, RW, RH, Angle float64 `json:"cx,omitempty" ...` // rotated_bbox 派生缓存（angle 弧度）
    Rings      [][][2]float64    `json:"rings,omitempty"`  // polygon：rings[0] 外环，其余洞/部件
    Attributes map[string]string `json:"attributes,omitempty"`
}

type Category struct { Key string; Index int; Name string; Color string; SuperCategory string; COCOID *int }
type Image struct { File string; Width, Height int; SHA256 string; Annotations []Annotation }
type Project struct { IRVersion string; Name string; Task string; Categories []Category } // Task: det|seg|obb|mixed
```

坐标一律绝对像素 float64；几何判别联合靠 `kind`；类别用稳定 key，index 只在导出瞬间映射。

### 2.2 校验与规范化

```go
type Issue struct { File string; Index int; Field string; Message string } // 可定位：哪张图第几条标注哪个字段

func Validate(p *Project, img *Image) []Issue      // 点数（poly 外环≥3、rotated=4）、类别 key 存在、坐标有限非 NaN、尺寸>0；越界坐标只警告不报错（Issue.Level? —— 用 Warnings 分离见下）
func Normalize(p *Project, img *Image) (warnings []Issue)
```

Normalize 职责（幂等）：
- rotated_bbox：四点排序统一（绕质心逆时针，起点为最左点，左同取上）；从 points 重算 CX/CY/RW/RH/Angle（w/h 取对边均值，angle 取 p0→p1 方向）；缺 points 有参数时由参数反推四点
- polygon：rings 按 |面积| 降序（最大环为 rings[0]）；面积用鞋带公式
- 坐标 clip 到 [0,width]×[0,height]，被 clip 的标注产生 warning（"第 N 条标注越界已裁剪"）
- bbox 的 X/Y/W/H 合法性（W/H>0，负值翻正并 warning）

### 2.3 文件布局与读写

```
<项目根>/project.json            # Project
<项目根>/ir/<图片相对路径>.json   # 每图一个 Image 文档（镜像目录结构，如 ir/sub/a.jpg.json）
```

```go
func WriteProject(dir string, p *Project, images []Image) error   // 原子写（tmp+rename，Windows 先去目标）；目录自动创建
func ReadProject(dir string) (*Project, []Image, error)           // project.json 不存在 → 特判 ErrNotProject；单图 JSON 损坏 → 返回错误带路径
```

## 3. internal/importer（W1 子代理 B/C）

统一出口（`internal/importer/dataset.go`，主代理落契约）：

```go
type Dataset struct {
    Categories []ir.Category
    Images     []ir.Image     // 含 Annotations；File 为正斜杠相对路径
    Warnings   []Issue        // 有损/降级提示（复用 ir.Issue）
}

type Options struct {
    Task        string // yolo 专用：det|seg|obb|auto；9 字段歧义（obb vs 4 点 seg）由此裁决，auto=按首行字段数猜（5→det，9→obb，其他→seg）
    VOCOneBased bool   // voc 专用：bndbox 按 1-based 减 1
}

func ParseYOLO(labelsDir string, dims map[string][2]int, opts Options) (*Dataset, []ir.Issue, error)
func ParseVOC(xmlDir string, opts Options) (*Dataset, []ir.Issue, error)
func ParseCOCO(path string) (*Dataset, []ir.Issue, error)
```

`dims`：rel path（正斜杠，与扫描一致）→ [w,h]，用于 YOLO 反归一化；labels 里找不到对应 dims 的 txt → Issue（跳过该文件）。返回的 []ir.Issue 是可定位解析错误（"第 37 张图第 4 行只有 2 个点"：File+Line+Message）；致命错误（目录不存在、JSON 完全损坏）才走 error。解析出的数据在返回前统一过 `ir.Normalize`，降级/有损进 Warnings。

### 3.1 YOLO（子代理 B）

- 接受**数据集根目录**（自动下钻最浅 labels/）或 labels 目录；txt 匹配 dims 的顺序：自身 stem → Ultralytics 镜像（最后一个 labels 段换 images）→ images/+自身 → 唯一后缀（有唯一性保护）
- names 发现：优先 data.yaml/data.yml（逐级父目录），否则该级唯一 *.yaml/*.yml（歧义放弃）；coco8.yaml 这类官方文件名可用
- 一图一 txt 镜像目录；每行 `class ...`：5 字段 det（cx cy w h 归一化→bbox）；9 字段且 Task∈{obb,auto} → rotated_bbox 四点；其余（≥7 且奇数? 实际 1+2n）→ polygon 单环。Task=seg 时 9 字段按 seg 解析
- 反归一化乘 dims；class id → category：`data.yaml`（labelsDir 同级或父级能找到时）解析 `names:`（支持 `names: [a,b]` 行内与 `0: a` 映射两种写法；**手写迷你解析，不加 yaml 依赖**）；无 data.yaml → 合成 `class_N`，key=name
- 空 txt = 无标注图片（合法，不产生 Issue）

### 3.2 VOC（子代理 B）

- 一图一 xml；`<size>` 给 dims（缺失或不匹配 → Issue 并跳过该文件）；`<object><name>` → category key=name
- `robndbox`（cx cy w h angle，弧度）优先于 `bndbox`；robndbox → rotated_bbox（参数→四点规范化）；bndbox → bbox，`VOCOneBased` 时四值减 1
- `difficult`/`truncated` → Attributes {"difficult":"1"} 等
- XML 解析用 encoding/xml；坏文件 → Issue 跳过

### 3.3 COCO（子代理 C）

- 单 JSON：`images[]`/`annotations[]`/`categories[]`；category id（可能 1-based 不连续）→ key 用 name，Index 按 categories 数组序重排，原 id 存 COCOID
- `bbox`=[x,y,w,h] 绝对像素 → bbox；`segmentation` 联合类型自定义 Unmarshal：
  - 多段多边形 `[[...],...]` → Rings（多段全部保留，Normalize 排序）
  - RLE `{"counts":[...], "size":[h,w]}`（数组 counts）→ 解掩码取外接 bbox + Attributes{"iscrowd":"1"} + Warnings 记降级
  - RLE 压缩字符串 counts → 实现 COCO 字符串 RLE 解码（~40 行，无依赖），同上降级 bbox
- iscrowd=1 的多边形也加 Attributes{"iscrowd":"1"}
- images 无对应 annotations → 空标注图片；annotations 引用不存在的 image/category → Issue

## 4. IR↔app 转换与导入应用（W2）

### 4.1 转换（`internal/app/irconv.go` 新文件）

| app ↔ IR | 规则 |
|---|---|
| rect, angle=0 ↔ bbox | cx/cy/w/h ↔ x,y,w,h（中心↔左上） |
| rect, angle≠0 ↔ rotated_bbox | corners() ↔ points；参数双向重算 |
| poly ↔ polygon | Points ↔ Rings[0]；导入时 rings>1 → 取最大环 + 有损 warning |
| Category | IR key↔name；导出 key=name（重复名加 `~2` 后缀）；导入按 §4.2 映射 |
| Attributes | 直通（W3 才有 UI；本波转换函数先带上，Ann.Attributes 字段 W3 才加——本波导入时属性暂存警告计数，不落库） |

### 4.2 导入向导（toolbar「导入标注」按钮，Modal 多步）

1. **格式**：YOLO / COCO / VOC / IR 单选；YOLO 加 task 选择（自动/det/seg/obb）；VOC 加 1-based 开关
2. **路径**：Dialog.Open 选 labels 目录（YOLO/VOC）或 JSON 文件（COCO）或项目根（IR，含 project.json）
3. **解析+类别映射**：解析结果预览（N 图 M 标注）；类别映射表：每个源类别 → 下拉选"现有类别 / 新建"；默认：同名→现有，否则新建
4. **错误与有损报告**：可定位 Issue 列表（滚动）；警告（降级/越界裁剪/多环损失）与错误分组；**有错误仍可导入正确部分**（错误条目跳过）
5. **应用**：写入 store（仅当前已打开目录里匹配 File 的图片；不匹配的图片列入警告）；undo 一条批量命令（整批可撤销）；setDirty 相关图

直接打开 IR：打开文件夹时若根下有 `project.json` 且无工作库标注 → 经同一导入管线（免向导，类别全新建）——复用遗留 JSON 迁移的位置，优先级低于遗留 JSON？不：project.json 优先（它是新格式）。细节：两者都存在时 project.json 优先。

### 4.3 测试

- 转换：三种 kind 往返（app→IR→app 恒等，误差<1e-9）；多环降级
- 应用管线：构造 Dataset → apply → store 读出核对；类别映射（复用/新建）；undo 一次全撤销
- 向导：Tester 驱动逐步点击（Modal 组件用法参照 W4 色板）；错误报告渲染

## 5. 属性与 obb 四点创建（W3）

### 5.1 标注属性

- store schema **v4**：`ALTER TABLE annotations ADD COLUMN attrs TEXT NOT NULL DEFAULT ''`（JSON object）；AnnRec 加 `Attrs map[string]string`；逐级迁移照旧
- app：Ann 加 `Attributes map[string]string`（json:"attributes,omitempty"）；annsToRecs/FromRecs 转换；undo 快照 cloneAnn 同步深拷贝 map
- IR 导入时 Attributes 落库（替换 W2 的"暂存警告"行为，若 W3 晚于 W2 合入则回填）
- **属性编辑 Modal**：双击已选中标注（单选）弹出：类别按钮组（点击改类）+ 属性键值对列表（每行 两个可编辑文本 + 删除钮 + 添加行钮；用 ui.EditableText/TextInput，参照 W4 结论）；确定/取消
- VOC difficult/truncated、COCO iscrowd 导入后可在 Modal 查看编辑

### 5.2 obb 四点创建（ToolObb）

- 工具栏加 [旋转框]（快捷键 O）；点击 4 次取四点（草稿渲染同 poly 创建），第 4 点即成框：w=|p1−p0|、h=|p3−p0|、angle=atan2(p1−p0)、中心=四点质心；Esc 取消、Backspace 删点（复用 poly 草稿机制）
- 已有 obb 的编辑不变（8 手柄+旋转柄）

## 6. 验收标准

1. 门禁全绿 + benchmark zoom1 1000 <16ms
2. ir：Validate/Normalize 表驱动（四点排序、环排序、clip 警告、NaN）；Write/ReadProject 往返
3. 三个导入器：fixture 驱动的解析测试（含畸形行、空 txt、RLE 两种 counts、robndbox 优先、1-based、iscrowd）；可定位 Issue 的 File/Line 断言
4. W2：转换往返、apply 管线、向导 Tester 流程、直接打开 IR
5. W3：attrs 存取往返、属性 Modal 编辑、ToolObb 四点成框（含与 poly 草稿互不干扰）
6. 手测：真实 YOLO/COCO/VOC 数据集各导入一次（用户验证）
