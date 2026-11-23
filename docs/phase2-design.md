# MyLabel 阶段 2 设计文档 — 标注画布核心（补全）

状态：**契约**。签名与交互以本文档为准。阶段 0/1 的铁律、坐标系、测试约定见 `AGENTS.md` 与 `docs/phase1-design.md`，本文不重复。

## 1. 范围与分期

| 波次 | 内容 | 文件归属 |
|---|---|---|
| Wave 1 | store schema v3（kind/points/类别颜色）+ 多边形纯几何 | A: `internal/store/*`；B: `internal/app/polygon.go`、`polygon_test.go`（新文件） |
| Wave 2 | seg 多边形全链路（模型/创建/编辑/命中/渲染/撤销/存取） | C: `internal/app/` 全包 |
| Wave 3 | 多选与框选（含批量删除/改类/整组移动） | D: `internal/app/` 全包 |
| Wave 4 | 类别面板（增删改名/颜色/数量不限）+ 图片状态标记 UI | E: `internal/app/` 全包 |

Wave 1 两个代理并行；之后每波一个代理串行，波间主代理 QC（build/vet/mygo vet/test 全绿才进入下一波）。

**移出本阶段**（计划书阶段 2 清单中的项，转阶段 3）：标注属性编辑 Modal（Ann 尚无属性字段，随 IR 属性一起做）；obb 四点创建（现有拖拽+旋转柄已覆盖 obb，四点法属增强）。

## 2. 数据模型（Wave 1 定契约，Wave 2 接线）

### 2.1 store schema v3（user_version = 3）

```sql
ALTER TABLE annotations ADD COLUMN kind   TEXT NOT NULL DEFAULT 'rect';  -- rect|poly
ALTER TABLE annotations ADD COLUMN points TEXT NOT NULL DEFAULT '';      -- JSON 扁平数组 [x0,y0,x1,y1,...]，图像像素
ALTER TABLE categories  ADD COLUMN color  TEXT NOT NULL DEFAULT '';      -- "#RRGGBB"，空=按序号取调色板默认色
```

v2→v3 用 `ALTER TABLE ADD COLUMN`（无需重建表）。v1 库先走 v1→v2 再走 v2→v3，逐级迁移。

- `kind='rect'`：cx/cy/w/h/angle 有效，points 为空；`angle=0` 即 det，否则 obb（不变）。
- `kind='poly'`：points 有效（≥3 点，隐式闭合），cx/cy/w/h/angle 冗余存外接框中心与宽高、angle=0（便于日后按框检索；以 points 为准）。

### 2.2 store API 变更（主代理已改签名，子代理 A 实现）

```go
const (
    KindRect = "rect"
    KindPoly = "poly"
)

type AnnRec struct {
    ID    string
    Cat   int
    Kind  string     // KindRect|KindPoly；输入空串视为 KindRect
    CX, CY, W, H, Angle float64
    Points []float64 // 扁平 [x0,y0,...]；KindPoly 必有，KindRect 忽略
}

type Category struct {
    Name  string
    Color string // "#RRGGBB"，空=默认调色板
}

func (s *Store) Categories() ([]Category, error)      // 签名变更（原 []string）
func (s *Store) SetCategories(cats []Category) error  // 签名变更（原 []string）
```

其余 API 不变。`ReplaceAnns` 负责 kind/points 的存取；`LoadImages` 回填。

### 2.3 app 模型（Wave 2 实施）

```go
const ( KindRect = store.KindRect; KindPoly = store.KindPoly ) // 或自定义等价常量

type Ann struct {
    ID    string  `json:"id"`
    Cat   int     `json:"cat"`
    Kind  string  `json:"kind"`           // "rect"|"poly"，导出 JSON 新增键
    CX, CY, W, H, Angle float64                  // rect
    Points []Point `json:"points,omitempty"` // poly，图像像素，隐式闭合
}

type Category = store.Category // app.categories 升级为 []Category（Wave 4 用颜色）
```

**深拷贝纪律**：Ann 带 slice 后，undo 命令快照（cmdCreate/cmdDelete/cmdModify）、draft 传递一律 `cloneAnn`（复制 Points）。这是本阶段最容易出事故的点，测试必须覆盖"撤销后原标注不被后续编辑污染"。

## 3. 多边形纯几何（Wave 1 子代理 B，`internal/app/polygon.go`）

签名（主代理已落 stub，不得改）：

```go
func pointInPolygon(p Point, poly []Point) bool                 // 射线法；len<3 → false；边界算在内（允许多像素误差内的命中，文档化即可）
func distPointSegment(p, a, b Point) float64
func nearestEdge(p Point, poly []Point) (edge int, dist float64) // edge = 边 (poly[edge], poly[(edge+1)%n])
func insertPointOnEdge(poly []Point, edge int, p Point) []Point  // 返回新切片，不改原切片
func removeVertex(poly []Point, i int) []Point                   // len<=3 返回 nil（不许破坏三边形）；返回新切片
func polygonBBox(poly []Point) (x, y, w, h float64)
func translatePolygon(poly []Point, d Point) []Point             // 返回新切片
```

纯函数、无 UI 依赖；测试表驱动（含凹多边形、自交接边界、水平边射线恰好过顶点的经典坑）。

## 4. seg 交互（Wave 2）

### 4.1 工具模式

App 新增 `tool`：`ToolRect`（默认）| `ToolPoly`。工具栏加 [矩形][多边形] 按钮（当前工具高亮）；快捷键 `R`/`P` 切换。既有矩形交互（创建/8 手柄/旋转柄）在 ToolRect 下完全不变。

### 4.2 多边形创建（ToolPoly，新状态 ModePolyCreate）

labelme 习惯：

- 左键空白处单击：开始草稿并追加顶点；之后每次单击追加一个顶点；橡皮筋线连到光标；已有顶点画小方块预览
- 闭合（≥3 顶点才允许）：单击距首顶点 **8 DIP** 以内 / 回车 / 双击 → 生成 `Ann{Kind: KindPoly}` + undo 命令 + setDirty
- `Escape` 放弃草稿；`Backspace` 删最后一个顶点
- 创建中滚轮平移、Ctrl+滚轮缩放、Space 抓手照常可用（草稿点是世界坐标，不受视口变化影响）

### 4.3 多边形编辑（选中态）

- 顶点手柄：小方块（边长 6 DIP），拖拽移动该顶点（undo cmdModify，深拷贝快照）
- `Ctrl+左键`点顶点：删除该顶点（剩余须 ≥3，否则忽略）
- 双击边：在该边上插入顶点（`nearestEdge` 定位）
- 拖拽内部：整体移动（`translatePolygon`）
- 多边形**没有** resize/旋转手柄
- 选中/取消、Delete 删除、1-9 改类别与矩形一致

### 4.4 命中优先级（hit.go 扩展）

顶点手柄 > 旋转柄（仅 rect）> resize 手柄（仅 rect）> 边（poly：5 DIP 内，用于双击插入的预命中，不改变光标行为亦可）> 内部（rect 用现有局部坐标判定；poly 用 `pointInPolygon`）。z 序倒序不变。

### 4.5 渲染（canvas.go 扩展）

- poly：`StrokePolyline`（闭合：末点连回首点）类别色描边 + 半透明填充（查 ui.Painter 的 Path/Fill 能力，无填充 API 则只描边，文档化）；选中态加粗 + 顶点方块
- 创建草稿：已确定顶点连线 + 光标橡皮筋 + 首顶点高亮圈（接近时放大提示可闭合）
- 性能铁律不变：单 Draw 循环；禁止一个标注一个 Element

### 4.6 存取

- `annsToRecs`/`annsFromRecs` 转换 kind/points；save→store→重开往返测试含 poly
- `exportJSON` 带 `kind`/`points`；遗留迁移的 JSON 全部按 rect 处理（无 kind 键即 rect）

## 5. 多选与框选（Wave 3）

- App 选择状态：`sel int` → `selSet map[int]bool` + `selPrimary int`（最后点中的，-1 无）；凡读 sel 的代码（canvas 高亮、Delete、1-9、拖拽）全部改造
- `Ctrl+点击` 标注：切换其选中态；普通点击：单选；点空白：清空
- `Shift+左键拖拽`空白：框选（屏幕矩形与标注外接框相交即选中；poly 用 points 的 bbox）——不与创建冲突（创建是无修饰键拖拽）
- `Ctrl+A` 全选当前图
- `Delete`：删除全部选中（**一条**批量 undo 命令，撤销一次全恢复）
- `1-9`：改全部选中的类别（一条批量 undo）
- 拖拽任一选中的标注：整组平移（rect 改 cx/cy，poly 平移 points；一条批量 undo）；resize/旋转只作用于单选（selPrimary 且 selSet 唯一）

## 6. 类别面板（Wave 4）

- 右侧固定宽栏（≈190 DIP）：类别列表（颜色块 + 名称，`curCat` 高亮，单击设当前类别）；底部 [添加][删除]
- 双击名称：行内 `TextInput` 重命名（Enter 确认/Esc 取消）
- 添加：名称自动 `catN`（N=现有数+1 起查重），颜色取调色板下一色；数量不限
- 删除：有标注引用时 `Dialog.Message` 阻止并显示引用数；否则删除并把 `cat>idx` 的标注 cat 减一（批量 undo 一条）
- 点击颜色块：Modal 12 色色板（预设色，非自由取色器）
- 调色板改生成式：黄金角 HSV 旋转（`catColor(i)` 先查自定义 Color，空则调色板）；保存时 `SetCategories` 带颜色
- 工具栏原 9 个类别按钮**移除**（面板取代）；快捷键 1-9 保留（作用于前 9 类）

## 7. 图片状态标记（Wave 4）

- `Ctrl+D`：当前图 `done ↔ new` 切换；某图首次产生标注编辑时 `new → labeling` 自动升级（setDirty 处）
- 状态变更即时 `SetImageStatus` 写库（单行更新，不走 debounce；同时计入 dirtyImages 之外，UI 状态栏不标 ●未保存）
- 缩略图单元格右上角圆点：labeling 蓝 / done 绿 / reviewed 紫 / skipped 灰；new 无点。reviewed/skipped 本阶段仅保留枚举，UI 只暴露 new/labeling/done

## 8. 验收标准（每波 QC + 总验收）

1. `go build ./...` / `go vet ./...` / `go tool mygo vet ./...` / `go test ./... -count=1` 全绿
2. seg：创建（点击/闭合/取消/删点）、顶点拖拽、Ctrl+点删顶点、双击插点、整体移动、命中、存取往返，全部有测试；undo 深拷贝污染测试
3. 多选：Ctrl+点/框选/Ctrl+A/批量删除/批量改类/整组移动 + 撤销恢复
4. 类别：增删改名换色、删除保护、cat 重映射、存取往返（含颜色）
5. 状态：Ctrl+D 切换、自动 labeling、缩略图圆点
6. 性能回归：1000 标注 zoom1 <16ms/帧（benchmark 复跑）
7. 手测（用户）：完整标注流程 + 重开不丢
