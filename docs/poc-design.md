# MyLabel PoC 架构设计（阶段 0）

> 作者：架构设计（主代理）；实现：coder 子代理；QC：主代理。
> 目标：验证 mygo 原生 UI（`ui` 包）做图像标注软件的关键技术点，代码即未来正式项目的骨架。
> 依据：`开发计划书.md` §3、§6 阶段 0。mygo 版本锁定 **v0.10.0**。

---

## 0. PoC 范围与验收标准

**最小链路**：打开图片文件夹 → GridView 缩略图条 → 画布显示大图 → 绘制/编辑可旋转矩形标注 → 保存/加载 JSON → `mygo build -tags=mygo_nowebview` 出 Windows 产物。

**必须验证的技术点（验收标准）**：

| # | 验证项 | 验收方式 | 通过标准 |
|---|---|---|---|
| 1 | 万级图片 GridView | 用 genimg 生成 10000 张 640×480 JPEG，人工 `mygo dev` 滚动 | 滚动流畅、内存可接受（状态栏显示 MemStats） |
| 2 | 大图与纹理上限 | 生成 8192×8192 与 10000×7500 图各一张；分块决策逻辑单元测试 | >8000px 走分块路径且能显示；≤8000 整图显示 |
| 3 | 千级标注绘制性能 |  Tester/benchmark 测量 Draw 循环帧耗时 | 1000 标注平均帧绘制 < 16ms（benchmark 输出，不作硬断言） |
| 4 | WindowToLocal 逆变换精度 | 单元测试：缩放+旋转组合下坐标往返 | 误差 < 0.5 图像像素 |
| 5 | 手柄命中+拖拽+旋转原型 | ui.Tester 模拟指针完成创建/移动/resize/rotate/undo | 测试全绿 |
| 6 | 保存/加载 | 原子写（tmp+rename）+ roundtrip 测试 | 崩溃安全、数据无损 |
| 7 | 打包 | `go tool mygo build`（GOFLAGS=-tags=mygo_nowebview，无 NSIS 则 -skip-installer） | 产出可执行文件 |
| 8 | WASM（可选） | `go tool mygo build -platform web` | 编译通过即可，不阻塞 |

**明确不做**（留给阶段 1+）：SQLite、seg/obb 完整编辑、导入导出三种格式、i18n、深色主题细节打磨、关闭守卫、菜单完备性。

---

## 1. 工程结构

项目根 = `C:\Users\admin\Desktop\MyLabel`（与规划文档共存）：

```
MyLabel/
├── go.mod                  # module mylabel；go 1.27.2；tool github.com/egoist/mygo/cmd/mygo；
│                           # require github.com/egoist/mygo v0.10.0、golang.org/x/image（webp/bmp DecodeConfig 用）
├── mygo.json               # 原生模板配置（字段以 docs-zh/configuration.md 为准）
├── main.go                 # 入口：WhenReady + NewWindow(Content: ui.View(...)) + App.Run()
├── main_test.go            # Tester 冒烟：应用能构建首帧
├── resources/icon.png      # 可用 Go 生成简单 PNG，或从 NetDream 复制
├── internal/
│   └── app/
│       ├── app.go          # App 状态结构 + 主视图装配（工具栏/画布/缩略图条/状态栏）
│       ├── canvas.go       # 画布 Element：Draw 回调（底图+标注层+手柄层）、Transform
│       ├── interact.go     # HandleInput 状态机：创建/选中/移动/resize/rotate/框外取消
│       ├── geom.go         # 纯几何：旋转矩形角点、局部坐标变换、缩放到光标、命中阈值
│       ├── hit.go          # 命中测试：手柄 > 边 > 内部；Path.Contains 备选
│       ├── undo.go         # 命令栈（Do/Undo）
│       ├── thumbs.go       # 缩略图后台生成 + 缓存（map[string]*ui.Bitmap）
│       ├── images.go       # 目录扫描 + DecodeConfig 读尺寸
│       ├── persist.go      # annotations.json 原子保存/加载
│       ├── tiles.go        # 大图分块决策 + 可见区域加载（ImageRegion）
│       └── *_test.go       # 各模块单元测试 + Tester 交互测试
└── cmd/
    └── genimg/main.go      # 测试图片生成器（-dir -n -w -h -big）
```

设计约束（mygo immediate-mode 铁律，QC 必查）：

1. **`ui.Element` 不得存入结构体字段/包变量/goroutine**；持久身份用 `ui.Handle`，跨帧状态用普通字段。
2. 后台 goroutine 改状态必须 `win.Update(func(){...})`；只重绘用 `win.Invalidate()`。
3. `Hovered()` 只允许在 `Draw` 回调内读取。
4. 缩略图缓存放 App 状态，**不放** GridView 行元素（行滚出视口会被回收）。
5. 标注渲染**禁止**一个标注一个 Element：全部在一个 `Box` 的 `Draw` 回调里循环绘制。
6. 写完跑 `go vet ./...` 和 `go tool mygo vet ./...`（若该命令存在）。

---

## 2. 数据模型（PoC IR-lite）

一种标注类型：**可旋转矩形**（`bbox` 为 angle=0 特例），同时验证 det 与 obb 的交互路径。

```go
// internal/app/model.go（可并入 app.go）
type Ann struct {
    ID    string  `json:"id"`
    Cat   int     `json:"cat"`              // 类别索引 0..8
    CX    float64 `json:"cx"`               // 图像像素坐标
    CY    float64 `json:"cy"`
    W     float64 `json:"w"`
    H     float64 `json:"h"`
    Angle float64 `json:"angle"`            // 弧度，绕中心
}

type ImageInfo struct {
    Path   string `json:"-"`                // 绝对路径（运行时）
    Name   string `json:"file"`             // 文件名
    Width  int    `json:"width"`
    Height int    `json:"height"`
    Anns   []Ann  `json:"anns"`
}

type docFile struct {                      // <dir>/mylabel.annotations.json
    IRVersion  string      `json:"ir_version"`      // "0.1-poc"
    Categories []string    `json:"categories"`      // 默认 ["cat1".."cat9"]
    Images     []ImageInfo `json:"images"`
}
```

坐标系：**图像绝对像素**为唯一真源；屏幕变换只存在于 `Transform`，指针经 `WindowToLocal` 逆映射。

---

## 3. 画布渲染（canvas.go）

一个 `Box` 承担全部绘制；`Transform` 顺序：**先平移后缩放**（pan 在屏幕空间，缩放以视图左上为原点，配合 zoomAtCursor 数学）。

```go
canvas := ui.Box(c).FillWidth().Grow(1).Clip()
canvas.Draw(func(p *ui.Painter, r ui.Rect) {
    p.Fill(r, c.Theme().Background, 0)
    if app.bmp == nil { /* 提示文字 */ return }
    p.Transform(ui.Translation(app.panX, app.panY).
        Then(ui.Scaling(app.zoom, app.zoom)), r.X, r.Y, func() {
        app.drawImage(p)                     // 整图或分块（tiles.go 决策）
        for i := range app.cur.Anns {
            drawAnn(p, &app.cur.Anns[i], i == app.sel, app.zoom)
        }
        if app.creating { drawAnn(p, &app.draft, -1, app.zoom) }   // 橡皮筋预览
    })
    app.drawHandles(p)                       // 手柄画在屏幕空间（尺寸不随 zoom 变）
})
```

- `drawAnn`：`p.Transform(Translation(cx,cy).Then(Rotation(deg)).Then(Translation(-w/2,-h/2)), ...)` 内 `Fill`（选中时半透明）+ `Stroke`；类别颜色取调色板 `catColor(cat)`。
- 底图绘制：`p.Image(bmp, ui.Rect{W: imgW, H: imgH}, ui.FillBox)`；分块时循环 `p.ImageRegion(tile, srcRect, dstRect, {})`。
- 线宽/手柄尺寸按屏幕恒定：线宽 `1.5/zoom`，手柄边长 `8/zoom`（在 Transform 内时）——手柄层画在屏幕空间则直接用 DIP。
- 禁止逐标注 `p.Text` 之外的优化暂不需要；标签文字用 `p.Text` 画类别名（1000 级标注若成为瓶颈，benchmark 里记录，后续换 `ui.Shape`+`Glyphs`）。

---

## 4. 交互状态机（interact.go）

状态枚举：`idle / creating / draggingMove / draggingHandle{annID, handle}`。输入经 `canvas.HandleInput(func(ev ui.InputEvent) bool)`：

| 事件 | 条件 | 动作 |
|---|---|---|
| PointerDown 左键 | 命中选中标注的手柄 | → draggingHandle（记录 ann 快照供 undo） |
| PointerDown 左键 | 命中某标注内部（不含手柄） | 选中该标注；→ draggingMove |
| PointerDown 左键 | 空白 | → creating（起点 = 当前点）；清除选择 |
| PointerDown 中键 | 任意 | → panning（记录起点） |
| PointerMove | panning | pan += (DX, DY) |
| PointerMove | creating | 更新 draft（W/H 随拖动；按住 Alt 以中心扩展） |
| PointerMove | draggingHandle | 按手柄类型更新几何（见 §5）；Shift 时 rotate 吸附 15° |
| PointerMove | draggingMove | 移动 CX/CY |
| PointerUp | creating | W 和 H 均 > 2px 则提交（入撤销栈），否则丢弃 |
| PointerUp | dragging* | 提交 undo 命令 |
| Scroll | Ctrl | zoomAtCursor |
| Scroll | 无修饰 | pan += (DX, DY) |
| KeyDown Delete/Backspace | 有选中 | 删除（入撤销栈） |
| KeyDown Escape | creating/dragging | 取消并回滚几何 |
| 数字键 1..9 | 有选中 | 改选中标注类别；否则设当前类别 |

坐标换算：**一切指针位置先经 `canvas.WindowToLocal(ev.X, ev.Y)` 得到图像像素坐标**，后续所有命中/几何计算都在图像像素空间完成（手柄阈值用 `handleDIP / zoom` 换算）。这保证缩放/旋转下逻辑一致，也是验证项 #4 的对象。

`zoomAtCursor(cx, cy, factor)`（geom.go，纯函数可测）：

```
// 光标下图像点在缩放前后保持不动
imgPt = (cursor - pan) / zoom
pan'  = cursor - imgPt * zoom'
zoom' = clamp(zoom * factor, 0.02, 64)
```

快捷键用 `c.Shortcut(ui.Cmd, ui.KeyS)`（保存）、`c.Shortcut(ui.Cmd, ui.KeyZ)`（撤销）、`c.Shortcut(ui.Cmd|ui.Shift, ui.KeyZ)`（重做）。`HandleInput` 对未消费事件返回 `false`，避免吃掉全局快捷键。

---

## 5. 旋转矩形几何与手柄（geom.go / hit.go）

**局部坐标系**：以中心为原点、按 -Angle 旋转。指针点 P（图像像素）→ 局部坐标：

```
dx, dy = P.X - CX, P.Y - CY
lx =  dx*cos(-A) - dy*sin(-A)
ly =  dx*sin(-A) + dy*cos(-A)
```

**手柄定义**（局部空间，8 个 resize + 1 个 rotate）：

```
角： (±W/2, ±H/2)                    → resize-corner（拖动改 W/H，对角固定）
边中点：(0,±H/2)、(±W/2,0)            → resize-edge（拖动只改对应维度）
旋转柄：局部 (0, -H/2 - 24/zoom)      → rotate
```

- resize：拖动角手柄 (hx,hy)（局部），新 W = 2*|lx|、H = 2*|ly|（对角固定于 (-hx,-hy)，随之重算中心：中心随动 = 对角点与新角点的中点，再旋转回世界坐标）。边中点只改一维。
- rotate：Angle = atan2(dy, dx) + π/2（使柄在正上方时为 0）；Shift 吸附 15°（π/12）。
- 命中优先级（hit.go）：**手柄（8+1，阈值 5/zoom px）> 已选中标注的边（阈值 3/zoom）> 标注内部**（点在局部矩形内即命中）；多标注重叠时按 z 序（数组倒序，后画优先）。
- 角点计算 `corners(ann) [4]Point`：局部四角旋转回世界，供绘制与测试使用。
- 光标反馈：命中角手柄 `ui.CursorResizeNWSE`/`NESW`（按 Angle 取近似方向）、边中点 `CursorResizeNS/EW`、旋转柄 `CursorPointer`、移动 `CursorMove`。用 `canvas.Cursor(...)` 在 Draw 回调里按 hover 命中结果设置（遵守铁律 3）。

---

## 6. 撤销/重做（undo.go）

命令栈，上限 200 条：

```go
type Command struct {
    Name string
    Do   func()
    Undo func()
}
// App.push(cmd)：执行 Do 并入栈，清空 redo 栈
```

命令种类（PoC）：CreateAnn、DeleteAnn、ModifyAnn（记录 before/after 快照，覆盖 move/resize/rotate/改类别）。连续微操作不合并（拖拽只在 PointerUp 提交一次，天然成单条命令）。

---

## 7. 图片加载与缩略图（images.go / thumbs.go / tiles.go）

**扫描**（images.go）：`Dialog.Open{Directory:true}` 选目录 → 非递归（PoC 简化）枚举 `.jpg/.jpeg/.png/.webp/.bmp/.gif`（大小写不敏感）→ 每个文件 `image.DecodeConfig` 读尺寸（注册 `_ "image/jpeg"` `image/png` `image/gif`、`golang.org/x/image/webp`、`golang.org/x/image/bmp`）→ `[]ImageInfo`。EXIF 处理 PoC 阶段依赖 `ui.DecodeBitmap` 自动摆正；记录为已知限制。

**缩略图**（thumbs.go）：

```go
// 切换目录后启动 worker 池（4 goroutine），按 GridView 可见优先可简化：
go func() {
    for _, img := range app.images {
        if app.thumbOf(img.Path) != nil { continue }
        data, err := os.ReadFile(img.Path)           // 大图分块：>64MB 直接跳过缩略图
        if err != nil { continue }
        bmp, err := ui.DecodeBitmapScaled(data, 256)
        if err != nil { continue }
        p := img.Path
        app.win.Update(func() { app.thumbs[p] = bmp }) // 铁律 2
    }
}()
```

`GridView(c, &app.grid, len(images), 96, 96, func(i){ ... })`，选中变化 → 加载主图。`GridState{Key: func(i) any { return images[i].Path }, Selected: &app.selImg}`。

**主图与分块**（tiles.go）：

```go
const tileThreshold = 8000        // 单边超过则分块（保守，低于文档 8192）
const tileSize = 2048

func loadMainImage(path string) error {
    data := os.ReadFile(path)
    w, h := decodeConfig(data)
    if max(w,h) <= tileThreshold {
        app.bmp = ui.DecodeBitmap(data)            // 自动 mipmap level
        app.tiled = nil
    } else {
        app.bmp = nil
        app.tiled = newTiledImage(data, w, h)      // 见下
    }
}
```

`TiledImage`：保留解码后的整图 `image.Image`（PoC 简化，内存换简单；QC 记录内存占用），按 tileSize 预切 `[]*ui.Bitmap`；`draw(p, viewRect)` 只 `ImageRegion` 可见 tile。全景缩略（zoom 很小）时用 `DecodeBitmapScaled(data, 2048)` 的概览图整幅绘制。测试：决策函数 `needTiles(w,h) bool` 单测覆盖 8000/8192/10000 边界。

**状态栏**：`runtime.ReadMemStats` 显示 Alloc MB + 图片数 + 当前 zoom，供验证项 #1 人工读数。

---

## 8. 持久化（persist.go）

- 路径：`<图片目录>/mylabel.annotations.json`
- 保存：`Ctrl+S` / 切换目录 / 退出前；写 `*.tmp` 后 `os.Rename`（原子）。`json.MarshalIndent`。
- 加载：打开目录时若存在则读取，按文件名匹配并入 `ImageInfo.Anns`；未知类别名→新建类别。
- 保存 debounce：标注变更后 2s 自动保存（`time.AfterFunc`，重置计时器）。

---

## 9. 测试计划（QC 执行依据）

`internal/app/geom_test.go`：
- `zoomAtCursor`：任意 (pan,zoom,cursor,factor)，断言光标下图像点不动（|Δ| < 1e-6 DIP）
- 局部坐标变换往返：随机 ann + 点，`toLocal`→`toWorld` 误差 < 0.5px（验证项 #4）
- `corners`：angle=0 时退化为轴对齐矩形

`internal/app/hit_test.go`：
- 手柄命中优先级：手柄点 vs 内部点 vs 外部点；阈值随 zoom 缩放
- 重叠标注 z 序

`internal/app/undo_test.go`：create/modify/delete 三连 undo/redo 回到初态。

`internal/app/persist_test.go`：临时目录 roundtrip；损坏 JSON 返回错误不 panic。

`internal/app/tiles_test.go`：`needTiles` 边界（8000 否 / 8001 是）。

`internal/app/interact_test.go`（ui.Tester，验证项 #5）：用 `ui.NewTesterWith(app.view, 1280, 800)`：
1. 注入两张内存图（`genTestImage` 直接构造 `ui.Bitmap`，不读盘）
2. `Press/Move/Release` 拖出一个矩形 → 断言产生 1 条 Ann，几何正确（换算 zoom=1）
3. 点击矩形内部 → 选中；拖动 → CX/CY 变化；undo → 还原
4. 拖动角手柄 → W/H 变化；拖动旋转柄 → Angle ≈ 预期（±0.01 rad）；Shift 吸附 15°
5. Delete 删除；undo 恢复
6. Tester 复用单实例（issue #1902 线程泄漏规避）；动画用 `tt.Advance` 不用 `time.Sleep`

`internal/app/draw_bench_test.go`：benchmark 1000 / 5000 标注的 Draw 循环（验证项 #3，输出数值不硬断言）。

`cmd/genimg/main.go`：`-dir -n 10000 -w 640 -h 480` 批量生成；`-big` 模式生成 8192×8192 与 10000×7500 各一张。JPEG quality 80，内容为渐变+伪随机色块（保证文件不雷同、大小真实）。

---

## 10. 构建与 QC 清单

实现完成后主代理执行：

```bash
cd C:\Users\admin\Desktop\MyLabel
go build ./...                 # 编译
go vet ./...                   # 静态检查
go tool mygo vet ./...         # mygo immediate-mode 误用检查（若命令存在）
go test ./...                  # 全部单测 + Tester 交互测试
go test -bench=. -run=^$ ./internal/app/   # 绘制性能数据
GOFLAGS="-tags=mygo_nowebview" go tool mygo build -skip-installer   # 无 NSIS 时
go run ./cmd/genimg -dir ./testdata/imgs -n 10000   # 生成验证数据（testdata 加入 .gitignore 或不入库）
```

代码审查要点（对照 §1 铁律 + 计划书 §3.1）：
- Element 无存储、goroutine 全部走 `win.Update`、`Hovered` 只在 Draw 内
- 坐标系：指针→`WindowToLocal`→图像像素，无屏幕/图像混算
- 原子保存、undo 栈上限、缩略图缓存放 App 状态
- 无未经规划的第三方依赖（go.mod 只允许 mygo + golang.org/x/image）

人工验证（交付给用户执行）：`go tool mygo dev` 打开 testdata/imgs，滚动缩略图条、绘制/旋转/删除标注、Ctrl+S、重开目录验证加载、观察状态栏内存。

---

## 11. 已知限制（PoC 不解决，记录在案）

- EXIF 方向：依赖 `DecodeBitmap` 自动摆正，未做导入时物理转正（阶段 1 处理）
- 目录扫描非递归；无文件监视
- 分块模式整图常驻内存（未做按视野懒加载原始区域）
- 标签文字在 5000+ 标注时可能成为瓶颈（benchmark 数据决定是否需要 Shape/Glyphs）
- 无 seg 多边形、无 IR 完整 schema、无格式导入导出
