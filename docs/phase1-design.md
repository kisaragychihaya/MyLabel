# MyLabel 阶段 1 设计文档 — 项目骨架与数据层

状态：**契约**。签名与数据流以本文档为准，实现必须逐字遵守导出 API。
阶段 0 的通用铁律（ui.Element 生命周期、win.Update、坐标系、测试约定）见 `AGENTS.md`，本文不重复。

## 1. 范围

| 项 | 内容 |
|---|---|
| SQLite 工作库 | `internal/store`：categories / images / annotations 三表 + WAL + user_version 迁移 |
| 图片导入管线 | `internal/imaging`：递归扫描 → JPEG EXIF 物理转正 → 读尺寸 → sha256 → （由 app 层）入库 |
| 存储切换 | App 工作数据从 `mylabel.annotations.json` 切换到 `<图片根>/mylabel.db`；JSON 降级为交换格式（遗留文件一次性自动迁移 + 工具栏"导出 JSON"按钮） |
| 关闭守卫 | 有未保存修改时 `Window.OnClose` 拦截，`Dialog.Message` 三选（保存并退出/不保存退出/取消） |
| CI | `.github/workflows/ci.yml`：build / vet / `mygo vet` / test，windows + ubuntu 矩阵 |

**不在本阶段**：图片状态标记的 UI（new/labeling/done… 字段入库即可，UI 属阶段 2）、缩略图落盘缓存（内存缓存已够用）、类别编辑 UI。

## 2. 包划分与文件归属（子代理并行边界）

| 子代理 | 拥有文件 | 依赖 |
|---|---|---|
| A: store | `internal/store/*.go`（store.go 补全 + store_test.go） | 无（只用契约） |
| B: imaging | `internal/imaging/*.go`（imaging.go 补全 + 测试） | 无 |
| C: app 集成 | `internal/app/*.go`、`main.go` | A、B 完成后的实现 |

A、B 并行；C 在 A、B 验收后启动。任何人不得改对方拥有的文件；契约文件里的签名不得改。

## 3. internal/store（子代理 A）

纯 Go `modernc.org/sqlite`，`database/sql` 驱动名 `sqlite`。Store 不是并发安全的：调用方（app 层）保证只在主线程使用；store 内部不加锁。

### 3.1 库文件位置

`<图片根目录>/mylabel.db`（WAL 模式会产生 `-wal`/`-shm` 伴生文件，正常）。

### 3.2 Schema（user_version = 2；v1→v2 重建 annotations 表去掉 id 全局主键）

> 注：阶段 2 已升级到 v3（annotations.kind/points、categories.color），见 `docs/phase2-design.md` §2。

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS categories (
  idx  INTEGER PRIMARY KEY,          -- 0-based 类别序号，即标注里的 cat
  name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS images (
  id         INTEGER PRIMARY KEY,
  rel_path   TEXT NOT NULL UNIQUE,   -- 相对图片根，正斜杠分隔（filepath.ToSlash）
  name       TEXT NOT NULL,          -- 文件名（base）
  width      INTEGER NOT NULL,
  height     INTEGER NOT NULL,
  sha256     TEXT NOT NULL,          -- 文件内容十六进制哈希（EXIF 转正后计算）
  status     TEXT NOT NULL DEFAULT 'new'  -- new|labeling|done|reviewed|skipped
);

CREATE TABLE IF NOT EXISTS annotations (
  id       TEXT NOT NULL,            -- ann_N，仅图内有意义，无唯一约束（v2 修正：v1 误设为全局 PRIMARY KEY，PoC JSON 跨会话复用 ID 导致迁移整体失败）
  image_id INTEGER NOT NULL REFERENCES images(id) ON DELETE CASCADE,
  cat      INTEGER NOT NULL,
  cx REAL NOT NULL, cy REAL NOT NULL,
  w  REAL NOT NULL, h  REAL NOT NULL,
  angle REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_anns_image ON annotations(image_id);
```

`Open` 负责建表与 `PRAGMA user_version = 1`；将来迁移按 user_version 递增。

### 3.3 导出 API（契约，`internal/store/store.go` 已落 stub）

```go
type AnnRec struct {
    ID    string
    Cat   int
    CX, CY, W, H, Angle float64
}

type ImageRec struct {
    ID      int64    // LoadImages 填充；UpsertImages 输入时忽略
    RelPath string   // 正斜杠
    Name    string
    Width, Height int
    SHA256  string
    Status  string   // 输入空串时入库为 'new'
    Anns    []AnnRec // 仅 LoadImages 填充
}

func Open(path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) Categories() ([]string, error)        // 按 idx 升序；无记录返回空切片
func (s *Store) SetCategories(names []string) error   // 全量替换（事务）
func (s *Store) UpsertImages(recs []ImageRec) error   // 按 rel_path 匹配：存在则更新 name/width/height/sha256（保留 status 与标注），不存在则插入；单事务
func (s *Store) DeleteMissingImages(keep []string) (int, error) // 删除 rel_path 不在 keep 中的图片（标注级联删除），返回删除数
func (s *Store) LoadImages() ([]ImageRec, error)      // 按 rel_path 升序；每条的 Anns 按 rowid 升序
func (s *Store) ReplaceAnns(imageID int64, anns []AnnRec) error // 该图标注全量替换（事务：DELETE + INSERT）
func (s *Store) SetImageStatus(imageID int64, status string) error
```

### 3.4 测试要求（store_test.go）

表驱动，库文件放 `t.TempDir()`。至少覆盖：Open 建库幂等（开两次不报错）；Categories 往返；Upsert 插入后再次 Upsert 同 rel_path 保留 status 与标注、更新尺寸；DeleteMissingImages 级联删标注；ReplaceAnns 全量替换语义；LoadImages 顺序与 Anns 回填；SetImageStatus。

## 4. internal/imaging（子代理 B）

### 4.1 导出 API（契约，`internal/imaging/imaging.go` 已落 stub）

```go
type ScannedImage struct {
    RelPath string // 相对 root，正斜杠
    Name    string // base 名
    AbsPath string // 绝对路径
    Width, Height int // EXIF 转正之后的像素尺寸
    SHA256  string // 转正之后的文件内容哈希
    Rotated bool   // 本次扫描对文件做了物理转正
}

// Scan 递归扫描 root（含子目录），识别 .jpg/.jpeg/.png/.webp/.bmp/.gif
// （大小写不敏感），按 RelPath 升序返回。
// 逐文件管线：JPEG 先 NormalizeOrientation → 读尺寸 → sha256。
// 单文件失败（打不开、解码不了）静默跳过；只有 root 不可读才返回 error。
func Scan(root string) ([]ScannedImage, error)

// NormalizeOrientation 读取 JPEG 的 EXIF Orientation（tag 0x0112）：
// 为 1 或缺失/非法 → (false, nil)，不动文件；
// 为 2..8 → 解码、按方向翻转/旋转、以 quality=95 重新编码 JPEG，
// 原子替换原文件（tmp+rename），返回 (true, nil)。
// 重新编码的 JPEG 不带 EXIF（方向已固化进像素，天然归零）。
// 非 JPEG 文件直接返回 (false, nil)。
func NormalizeOrientation(path string) (rotated bool, err error)

// Orientation 返回 JPEG 的 EXIF Orientation；缺失/非法/非 JPEG 返回 (1, nil)。
func Orientation(path string) (int, error)

// HashFile 返回文件内容的 sha256 十六进制小写。
func HashFile(path string) (string, error)
```

### 4.2 实现要点

- **不加新第三方依赖**。EXIF 方向手写解析：JPEG 以 `FFD8` 开头，遍历 segment（`FF Ex` + 2 字节大端长度），找 APP1（`FFE1`）且 payload 以 `Exif\0\0` 开头；其后是 TIFF 头（`II*\0` 或 `MM\0*` 决定字节序），IFD0 偏移在 TIFF 头 +4；逐项（12 字节）找 tag `0x0112`，取 SHORT 值。只读前 64KB 足够（EXIF 必在文件头）。
- 8 个方向的像素变换：2=水平翻转，3=旋转180，4=垂直翻转，5=转置（主对角线），6=旋转90 CW，7=反对角转置，8=旋转90 CCW。用标准库 `image/draw` 逐目标像素映射即可，文件级操作不需要 SIMD。
- 原子替换：写 `<path>.tmp-mylabel` → 关闭 → `os.Rename`。Windows 不能覆盖 rename，先 `os.Remove` 原文件（与 persist.go 既有做法一致）。
- 尺寸读取：`image.DecodeConfig`，包内注册解码器（`_ "image/jpeg"` 等 + `golang.org/x/image/webp|bmp`），与 PoC `images.go` 相同。
- 目录遍历：`filepath.WalkDir`。不跟踪符号链接目录（WalkDir 默认行为即可）。隐藏目录不特殊处理。

### 4.3 测试要求

- EXIF 全部 8 个方向：测试内程序生成小 JPEG（如 6x4，像素各不相同），手工拼 APP1 EXIF 头写入文件，NormalizeOrientation 后断言：Rotated=true、尺寸符合预期（6/7/8 方向宽高互换）、像素级断言角点颜色（用 image 解码比对）。
- 无 EXIF / Orientation=1 / 非 JPEG：返回 false 且文件字节不变。
- Scan：t.TempDir() 造嵌套目录（含子目录里的图片、非图片文件、坏文件），断言递归发现、RelPath 正斜杠、排序、坏文件跳过。
- HashFile 已知内容对已知哈希。

## 5. app 集成（子代理 C）

### 5.1 数据模型变更（model.go）

```go
type ImageInfo struct {
    ID      int64  `json:"-"`     // store 行 id，运行时
    Path    string `json:"-"`     // 绝对路径，运行时（= dir + RelPath）
    RelPath string `json:"file"`  // JSON 交换格式键名不变，值从"裸文件名"变为"正斜杠相对路径"
    Name    string `json:"-"`     // base 名（显示用）
    Width, Height int `json:"width","height"` // json tag 保持
    Status  string `json:"status,omitempty"`
    Anns    []Ann  `json:"anns"`
}
```

（以上是意图说明；实现时保持现有 json tag 兼容性即可，细节自洽。）

App 新增字段：`store *store.Store`、`dirtyImages map[int64]bool`（记录哪些图的标注需要落库；setDirty 处同步登记当前图 id）。

### 5.2 openDir 新流程

1. `_ = a.save()` 冲刷上一个目录；关闭上一个 store（`a.store.Close()`）。
2. `imaging.Scan(dir)`（递归）。失败 → 状态栏报错返回。
3. `store.Open(filepath.Join(dir, "mylabel.db"))`。失败 → 报错返回。
4. `UpsertImages(扫描结果)` + `DeleteMissingImages(本次扫描到的 rel 集合)`。
5. 遗留迁移：若库中图片没有任何标注且 `<dir>/mylabel.annotations.json` 存在 → `loadDoc` + 按 `file` 匹配（旧文件里 file 是裸文件名，匹配规则：`RelPath == docImage.Name || base(RelPath) == docImage.Name` 且唯一）写入 ReplaceAnns + SetCategories。迁移成功后在状态栏提示"已迁移旧 JSON 标注"。**不删旧 JSON 文件**。
6. `LoadImages()` → 组装 `a.images`（Path = join(dir, RelPath)）。
7. 之后流程不变（grid、缩略图、加载第一张）。

### 5.3 save 新流程

- `a.store == nil` → nil（未打开目录）。
- 事务语义由 store 方法保证：`SetCategories(a.categories)`；对 `dirtyImages` 里每个 id `ReplaceAnns`；清空 dirtyImages；`a.dirty = false`。
- 旧的 JSON 全量写逻辑（`docFile` 拼装 + 原子写）改名为 `exportJSON(dir string) error`，仅在"导出 JSON"按钮和遗留迁移中用到加载侧。
- 工具栏加按钮 **"导出 JSON"**：调 `exportJSON(a.dir)`，写 `<dir>/mylabel.annotations.json`（内容格式与 PoC 相同），状态栏提示。
- 失败处理保持现状（状态栏 + lastStatus）。

### 5.4 关闭守卫

`main.go`：`NewWindow` 返回的窗口存到变量，`win.OnClose(func(e *mygo.CloseEvent) { if !a.ConfirmClose() { e.PreventDefault() } })`。

`internal/app` 新增（如 close.go）：

```go
// ConfirmClose 在未保存修改时弹三选对话框；返回 true 允许关闭。
func (a *App) ConfirmClose() bool {
    _ = a.save()              // 先尝试静默落库（debounce 定时器停掉、dirtyImages 写入）
    if !a.dirty { return true }  // 保存成功或无脏数据
    res, err := mygo.Dialog.Message(mygo.MessageOptions{
        Parent: a.win, Type: mygo.MessageWarning,
        Title: "未保存的修改", Message: "标注尚未保存，退出前是否保存？",
        Buttons: []string{"保存并退出", "不保存退出", "取消"},
        DefaultButton: 0, CancelButton: 2, DestructiveButtons: []int{1},
    })
    if err != nil { return false }
    switch res.Button {
    case 0: return a.save() == nil
    case 1: return true
    default: return false
    }
}
```

注意：`save()` 成功后 dirty=false，所以正常路径根本不会弹窗；只有保存失败才弹。测试时用无窗口路径（win==nil 时不弹窗直接返回 save 结果）覆盖。

### 5.5 既有代码处置

- `images.go`：`scanImages`/`decodeConfigSize` 被 `imaging.Scan` 取代，删除该文件；其测试并入 imaging 的测试（子代理 B 已覆盖等价用例后，app 侧删除 images_test.go）。注意 `imageExts` 的引用清干净。
- `persist.go`：保留 `loadDoc`（迁移用）与原子写，拆出 `exportJSON`；`save`/`scheduleSave` 改为 store 路径。
- `thumbs.go` / `tiles.go` / `canvas.go` / `interact.go` / `hit.go` / `geom.go` / `undo.go` / `drop.go`：原则上不动。drop.go 打开文件夹走 openDir，自动获得新流程。
- **所有既有测试必须保持绿色**；行为确实变化（如递归扫描、JSON 键含义）允许改测试，但要在交付说明里逐条列出。
- 状态栏微调：加一行"已迁移"/"已转正 N 张"提示（来自 Scan 结果的 Rotated 计数，openDir 汇总）。

### 5.6 交互/展示约束

- RelPath 含子目录时缩略图条显示 Name（base 名）即可；状态栏 cur 显示 RelPath。
- 打开目录后焦点落画布、Space 抓手等 labelme 交互一律不变。

## 6. CI（主代理已落）

`.github/workflows/ci.yml`：windows-latest + ubuntu-latest，Go 1.27，`go build` / `go vet` / `go tool mygo vet` / `go test -count=1`。dist/testdata 不入库（.gitignore 已有）。

## 7. 验收标准

1. `go build ./...`、`go vet ./...`、`go tool mygo vet ./...`、`go test ./... -count=1` 全绿。
2. 手测：打开 testdata/imgs → 标注 → Ctrl+S → 目录下出现 mylabel.db（无 JSON）；重开 → 标注还在；关窗有未保存修改且保存失败时弹三选。
3. 递归：打开带子目录的图片根，子目录图片出现且标注可保存/重载。
4. EXIF：测试构造方向 6 的 JPEG，打开后文件被物理转正一次，再次扫描不再 Rotated。
5. 导出 JSON：按钮写出与 PoC 同构的 mylabel.annotations.json。
