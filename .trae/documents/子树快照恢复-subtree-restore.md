# 从完整快照恢复单个章节（Subtree Restore）

## Context

LeafWiki 现有恢复能力只有**整站 swap**（[manager.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/restore/manager.go) → `SwapAll` 替换 root/、assets/、所有 .db 并重启会话）。用户需要"只恢复一个章节"：

- 章节内页面内容与层级（目录父子关系 + `.order.json` 兄弟排序，含章节根在父级中的位置）回到指定快照状态；
- 章节外内容（兄弟页面、assets、branding、所有数据库、settings）零影响；
- 当前站点对该章节的任何人工偏离都**先报冲突、整体拒绝**（零写入），确认后可 force 执行；
- 同一清单重跑结果一致（成功后重跑为 no-op）；
- 服务中途退出，重启后自动"完成或完整回退"，不允许整站恢复那种 NeedsIntervention 手工干预状态。

### 已确认决策

1. **冲突策略=严格**：①内容被编辑 ②节点被移动 ③页面被删除 ④章节下有新增文件 ⑤page↔section 类型变化，任一出现即 409 冲突清单、零写入；`force=true` 才能强制执行。
2. 章节用**快照 frontmatter 中的 `leafwiki_id`（节点 ID）**指定。
3. 交付**后端 + admin HTTP API + Go 测试**，不做前端。
4. **不恢复 assets/**，只动 `root/` 下章节相关 md / index.md / `.order.json`。

### 关键既有事实（复用点）

- 快照 ZIP：`<backupsDir>/<id>.zip`，含 `root/` 整树 + `backup-meta.json`；[SnapshotZipPath](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/snapshot/manager.go#L223-L247) 是 ID 校验/路径穿越边界。
- 磁盘模型：page=`<path>.md`，section=`<path>/index.md` + 同目录 `.order.json`（`{"ordered_ids":[...]}`）；受管 frontmatter 含 `leafwiki_id/title/created_at/updated_at/creator_id/last_author_id/pinned`。
- [ReconstructTreeFromFSContext](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/core/tree/tree_service.go#L162-L192)：从 root/ 无锁扫盘重建整树再加锁换内存树；对任意含 `root/` 的 storageDir 都可用（快照 staging 同理）。**注意副作用**：缺 index.md 的 section 会被物化、缺 frontmatter 会回写——故"期望字节"必须以 **ZIP 原始条目字节**为准，不能用 staging 重建后的文件。
- 整站恢复提供可直接复用的模式：[WriteGate](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/restore/writegate.go)（Engage/WaitForDrain，已挂 503 中间件）、zip-slip 安全解压（[extractZipEntry](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/restore/swap.go#L116-L151)）、[Job](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/restore/job.go) 单飞异步状态、路由样板 [internal/wiki/restore/routes.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/wiki/wiki/../restore/routes.go)（admin + cookie session、202 + status 轮询）。
- 恢复后派生索引用现成的 `w.TriggerResyncAsync()`（tree/links/tags/search 重建）；resync **不重建 revisions**（修订在 `<dataDir>/.leafwiki/`，与 root/ 独立）。
- `revision.Service` 已有未导出的 [recordRestoreRevision](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/core/revision/service.go#L1072)，导出即可在换树后补记一条 `RevisionTypeRestore`。
- 启动顺序：[main.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/cmd/leafwiki/main.go#L212-L250) L218 dataDir 就绪 → L245 git backup 同步 → L250 `wiki.NewWiki`（内含 LoadTree）。崩溃恢复必须在 L245 之前收敛磁盘。

## 方案总览

新增两层（形状对齐 `internal/restore` + `internal/wiki/restore`）：

- **`internal/subtreerestore`**：核心引擎（解压、双树 diff、manifest、journal、崩溃恢复、异步 Manager）。只依赖 `internal/core/*`（tree/ignore/shared）+ 标准库，过 `internal/architecture_test.go` 边界；不 import snapshot/restore/wiki，靠接口解耦：

```go
type SnapshotLocator interface { SnapshotZipPath(id string) (string, error) } // *snapshot.Manager 满足
type WriteGate interface { Engage(); Disengage(); Engaged() bool; WaitForDrain(time.Duration) bool } // *restore.WriteGate 满足
type Config struct {
    DataDir        string
    Snapshots      SnapshotLocator
    Gate           WriteGate
    ReloadTree     func() error    // w 的 tree.ReconstructTreeFromFS
    TriggerResync  func()          // w.TriggerResyncAsync
    RecordRestore  func(pageIDs []string) // 可选，revision 关闭时 nil
    WikiVersion    string
}
```

- **`internal/wiki/subtreerestore`**（package `wikisubtreerestore`）：gin 路由 + 错误码映射。

## 核心算法

### 1. 子树枚举

1. 只解压 ZIP 中 `root/` 前缀条目 + `backup-meta.json` 到 dataDir 内临时目录 `.leafwiki-subtree-stage-*`（同盘，后续 rename；可信快照用 `shared.UnrestrictedExtractionLimits`，对齐 `runLocked`）。流式对每条记录算 sha256，建 `zipFiles: rel(去掉 root/) → {hash,size}`。
2. 在 staging 上 `tree.NewNodeStore` + `ignore.NewCache(staging+"/root")` 重建**快照树**，DFS 按 ID 找章节根 R；`root` 拒（整树请用整站恢复），找不到 → section_not_found。
3. 期望文件集 D：
   - R 为 section：`CalculatePath(R)/` 前缀下所有 ZIP 文件（含各层级 `index.md`、`.order.json`）；R 为顶层 page：仅 `foo.md`。
   - 额外纳入**章节根父目录的 `.order.json`**（章节根位置要恢复；R 顶层时即 `root/.order.json`），其内容由合并算法合成（见下），不取 ZIP 原文。
   - 排除：除 `.order.json` 外所有 `.` 开头文件；按 **live 当前 `.leafwikiignore`**（唯一权威，ignore 规则本身不恢复）命中的路径——两侧对称排除，保证章节外/被忽略文件永不动。

### 2. Plan / 差异（纯函数，确定性）

- live 侧：一次性新建临时 NodeStore 重建 **live 树**（只读），按 ID 与快照子树节点对齐；按 rel 直接读盘算 hash；对章节前缀做 Walk 得 live 原始文件集 L（同样排除规则）。
- 冲突分类（全部进 409 清单，零写入）：

| code | 判定 |
|---|---|
| `page_deleted` | 快照子树节点 ID 在 live 树不存在 |
| `kind_changed` | 两边都有但 kind 不同 |
| `node_moved` | kind 同但 CalculatePath 不同（含改名/换父） |
| `content_edited` | id/kind/path 同但 raw 文件 sha256 不同（含子树内非 md 原始文件） |
| `extra_file` | 章节前缀下 live 有、D 无的任何文件/目录 |

- 子树**内部** `.order.json` 差异不报冲突（排序回正是本功能职责，直接出 `write_order`）。特例：快照 section 的 index.md 在 ZIP 缺席而 live 有自动物化副本 → 跳过（噪声）。
- 无冲突时动作清单：`write`（新建/覆盖）、`write_order`（内部 order 直接用 ZIP 字节）、`delete`（**仅 force 清单**出现 extra_file 时）。
- **父级 order 合并**（不能整文件覆盖快照版，否则重排章节外兄弟）：取快照父级子序列中 R 之前、且 live 仍存在的兄弟数 k；从 live ordered_ids 去掉 R 保持其余相对顺序，在第 k 个共同兄弟后插回 R；live 有但序列缺的 ID 附末尾。合成结果与 live 语义相同则 no-op。
- force 跨目录：`node_moved` → snap 路径 write + live 旧路径 delete + 旧父 order 剔除该 ID；`kind_changed` → section→page 时目录内文件逐项 delete、空目录记 journal 后删除。

```go
type Item struct {
    Op               Op     `json:"op"`     // write | delete | write_order
    Rel              string `json:"rel"`    // 相对 root/
    NodeID           string `json:"nodeId,omitempty"`
    SnapshotEntry    string `json:"snapshotEntry,omitempty"` // zip 内 root/<rel>
    ExpectedLiveHash string `json:"expectedLiveHash"` // ""=执行前必须不存在
    DesiredHash      string `json:"desiredHash"`      // ""=删除
}
```

Manifest hash = sha256(canonicalJSON{snapshotId, sectionId, items 按 rel 排序})；与遍历顺序无关。

### 3. 执行与 journal（崩溃安全）

布局（全在 dataDir 同盘，全部 os.Rename）：

```
<dataDir>/.leafwiki/subtree-restore/<jobID>/
    journal.json
    staged/<rel>     # 期望字节（父级 order 为合成内容）
    backup/<rel>     # move-aside 的原文件（被替换 + 被删除项）
```

journal：`state ∈ prepared|applying|applied|rolling_back|committed`，含 items（每项 Applied 标记）、`CreatedDirs`、`RemovedEmptyDirs`；每次状态翻转/每项完成都用 `shared.WriteFileAtomic` 重写。

执行序列（异步单飞 job，phase: extracting→planning→preparing→applying→reloading）：

1. 解压、重建快照树、重新 plan（execute **永远从地板重算**，plan 端点仅预览；带 `manifestHash` 但不一致 → `plan_mismatch`）。
2. 冲突且非 force → 失败返回，零写入。
3. preparing：期望字节落 staged/，写 `prepared`。
4. `Gate.Engage()` + `WaitForDrain(10s)`（超时 warn，沿用整站恢复）。
5. **地板复核（force 也不豁免）**：逐项校验 live hash == ExpectedLiveHash（空=必须不存在）；不符 → disengage、失败、零写入。
6. `applying`：先建父目录（记 CreatedDirs）；write/delete 都先 `rename(live→backup)` 再 `rename(staged→live)`，每项持久化；类型翻转产生的空目录记 RemovedEmptyDirs 后删除。Windows 安全：目标永远先移走。
7. `applied` → `ReloadTree()`（同步换内存树；失败走回滚）→ `TriggerResync()`（异步重建 links/tags/search）→ `committed` → disengage。
8. committed 后对"内容确实变化的节点文件"调 `RecordRestore(pageIDs)`（`RevisionTypeRestore`，author=`system`；单页失败只 warn，宁可漏记不重复；被删页不记）。
9. 清理 job 目录与 staging。

**回滚**（运行期失败，逆序、只信磁盘、可重入）：backup 存在 → 校验 live==DesiredHash 后删 live、rename backup 回去（live 被外来改写则中止保留现场）；backup 不存在的新建项 → 删 live；重建被删空目录、从深到浅清理建出的空目录（非空保留）。

**`Recover(dataDir)`（启动期 main.go L218 之后、L245 之前调用，失败则拒绝启动）**：
- 无目录 → no-op；
- `prepared/applying/rolling_back` → 完整回滚；
- `applied` → 前向补齐（按磁盘实况重做缺失的 rename-in，文件收敛即可，后续冷启动 LoadTree + 启动期索引重建天然完成 reloading/resync）→ committed；
- `committed` → 清理；重复执行幂等；无法收敛 → 返回错误中止启动（优于带歧义启动）。

整站恢复与子树恢复共用同一 WriteGate：gate 已 engaged 时 execute 返回 409；存在未收敛 journal 时 execute 返回 `subtree_restore_recovering`（需重启先 Recover）。

### 4. 幂等

- 成功后重跑：所有文件 expected==desired、无 extra、父级 order 语义相同 → 空 manifest → 不 engage gate、直接成功 `noop:true`；
- plan 只读、staging 用完即删；
- 失败回滚后 journal 清除，重跑等价首次；plan 是同 ZIP+同地板的纯函数，manifest hash 一致。

## HTTP API（挂 `/api/admin`，RequireAdmin；POST 另加 RequireCookieSession）

- `POST /subtree-restore/plan` `{snapshotId, sectionId}` → 202；结果轮询 status（kind=plan，含 section 信息 + conflicts + manifest 预览）。
- `POST /subtree-restore/execute` `{snapshotId, sectionId, force, manifestHash?}` → 202；冲突/不匹配经 status.errorCode=409 类 code 体现。
- `GET /subtree-restore/status` → `{running,done,kind,phase,snapshotId,sectionId,section,conflicts[],manifest{hash,itemCount,counts},noop,error,errorCode}`。
- `GET /subtree-restore/snapshots/:id/tree?sectionId=` 同步只读：返回快照目录树（id/title/kind/slug/path/children），供后续前端选择章节。
- manager=nil（快照功能关闭）→ 503。错误码→状态：already_running/conflicts/plan_mismatch/recovering →409，section_not_found/snapshot_not_found→404，invalid_section/snapshot_invalid_id→400。

## 实现步骤（Conventional Commits，建议两个 PR）

**PR A — 引擎（无 HTTP/接线）**

1. `refactor(restore): extract zip entry helper into core/shared`：把 extractZipEntry 实现移到 `internal/core/shared/zip_extract.go`，swap.go 委托调用，测试不变。
2. `feat(subtree-restore): add root-only snapshot extraction with entry hashes`（extract.go）。
3. `feat(subtree-restore): reconstruct snapshot tree and enumerate section subtree`（snapshot_tree.go）。
4. `feat(subtree-restore): add plan/diff engine, parent-order merge and deterministic manifest`（plan.go + 全部冲突分类单测）。
5. `feat(subtree-restore): add journaled applier with rollback and boot recovery`（journal.go + manager.go 雏形）。
6. `feat(subtree-restore): add async manager, job phases and localized errors`。
7. `docs: add ADR for journaled subtree restore`（journal 状态机、掉电=fsync 但不 fsync 父目录的威胁模型、gate 下读窗口、ignore/点文件边界、revision 取舍）。

**PR B — API 与接线**

8. `feat(revision): export RecordRestoreRevision`。
9. `feat(wiki): add subtree restore registrar seam and reload callbacks`：wiki.go/wiki_routes.go 加字段 + `SetSubtreeRestoreRoutes` + Registrars 追加；新增 `(*Wiki).ReloadTree()` 与 `(*Wiki).RecordRestoreRevisions([]string)`。
10. `feat(subtree-restore): add admin HTTP API`（slice routes.go/errors.go + routes 测试）。
11. `feat(main): wire subtree restore and run recovery before boot`：main.go 在 L218 后调 `subtreerestore.Recover(dataDir)`；L312 snapshot 块内复用 snapshotManager + writeGate 构造 Manager 并注册，`defer manager.Wait()`。
12. `test: tidy`。

## 关键文件

- 新增：`internal/subtreerestore/{config,extract,snapshot_tree,plan,journal,manager,errors}.go` + `_test.go`；`internal/wiki/subtreerestore/{routes,errors}.go` + `routes_test.go`；`docs/development/adr/0014-journaled-subtree-restore.md`。
- 修改：[internal/restore/swap.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/restore/swap.go)（提取共享 helper）、[internal/core/revision/service.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/core/revision/service.go#L1072)（导出）、[internal/wiki/wiki.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/wiki/wiki.go) / [wiki_routes.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/wiki/wiki_routes.go)、[cmd/leafwiki/main.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/cmd/leafwiki/main.go)。
- 测试夹具参照 [internal/restore/helpers_test.go](file:///Users/zhangchengcheng/trae/leafwiki-subtree-restore-reconcile-T03/internal/restore/helpers_test.go)（真实 snapshot.Manager.RunOnce 造 ZIP、手写带 frontmatter 的 md）。

## 验证

**单测（`go test ./internal/subtreerestore/... ./internal/wiki/subtreerestore/...`）**

- 五种冲突各一：内容编辑/移动/删除/新增（含非 md 与 `.DS_Store`、`.leafwikiignore` 命中项不噪）/kind 双向；非 force 逐文件 hash 前后不变，force 全部落地。
- 无冲突恢复：嵌套层级、多级 order、章节根父级位置、顶层章节（root/.order.json）、章节根是单 page；断言章节外文件（兄弟 md、assets、branding、*.db、settings）字节不变。
- 父级 order 合并表驱动（章节根在头/中/尾、live 有快照无的兄弟、快照无 order 文件）。
- 幂等：成功后（force 与非 force）重跑 noop；plan 重复调用；manifest hash 确定性。
- plan→execute 间篡改地板 → 冲突失败零写入（即便 force）。
- 崩溃：prepared / applying 中段 / applied / committed 四个 kill 点后直接调 `Recover()`：前两者与恢复前字节一致，后两者等于快照且无残留；Recover 连跑两次一致。
- 快照缺 section index.md 而 live 有物化副本 → 不报冲突。
- RecordRestore 收到的 pageID 集合正确（fake 回调）。
- 路由：非 admin 403、API key POST 被拒、manager=nil 503、202→status 轮询 happy path、409 冲突结构、404/400/409 错误码。

**门禁**：`go test ./...`、`golangci-lint run`、`GOOS=windows go build ./...`、`npm run build`（不应触及前端，确认无 i18n key 等影响）。e2e 暂不加（无前端入口；Playwright 打共享真实实例且破坏性，留待前端交付）。
