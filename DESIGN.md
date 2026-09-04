# Installer 设计

## 目标与领域模型

Installer 将命名空间内的 `Instance` 具化为 Kubernetes 资源，并把观察到的运行状态维护在 `Instance` status 中。

- **Instance** 是期望的安装声明，包括来源、values、依赖、扩展、生命周期选项和实例级副本数。
- **来源**是集群内不可变 Artifact 或基于 URL 的 bundle；由选中的 installer 判断能否消费来源内容。
- **渲染资源**是来源生成、尚未应用平台不变量的 Kubernetes 对象。
- **受管资源**是已经成功纳入 Instance 生命周期并记录在 `status.resources` 中的渲染资源。
- **运行时 values** 是解析后的用户 values 与 `global.replicas` 等 installer 自有 values 的合并结果。
- **暂停**是受管工作负载的执行控制，与期望副本数相互独立。

Status 表达观察结果而非期望配置。`status.values` 记录最近一次成功 apply 使用的 values；states、endpoints、summary、副本数和 conditions 描述之后对受管资源的观察。

## 所有权与 seam

| 模块 | 所有权 | 接口或 seam |
| --- | --- | --- |
| `apis/apps/v1` | Instance 资源序列化契约和状态词汇 | Kubernetes CRD 与 scale subresource |
| `controller` | Reconcile 顺序、依赖门控、values 与凭据解析、执行决策和状态投影 | `InstanceReconciler` |
| `controller/postrender` | 所有渲染对象必须满足的平台不变量 | `install.PostRenderer` / `ObjectModifier` |
| `install` | 安装输入、结果、生命周期策略和 installer 接口 | `Installer` |
| `install/delegate` 与 `install/download` | 来源选择、获取、校验和 installer 选择 | 传给 installer 的本地 bundle 路径 |
| `install/helm` | Helm release 生命周期 | `Installer` adapter |
| `install/native` | 资源清单 diff 与直接 Kubernetes apply/remove | Kustomize 和 Template 模式使用的 `Installer` adapter |
| `controller/instance-status.go` | 运行阶段、conditions、states、endpoints、summary 和 scale 观察 | Instance status |
| `controller/dynamicsources.go` | 受管资源种类产生的 reconcile 事件 | 受管资源身份索引 |

Controller 是编排所有者。安装 adapter 不推断 Instance 策略，controller 也不重复实现 Helm 或 native apply 机制。

## 规范 Reconcile 流程

一次 reconcile 按以下顺序执行：

1. 加载 Instance 并建立 finalizer。
2. 在执行一个尚未成功安装的 generation 前，观察所有声明的依赖。依赖缺失或未就绪时投影为 `Waiting` 和 false dependency condition，并成功结束本次 reconcile。依赖 Instance 变化会将当前 generation 尚未成功安装的被依赖方加入队列；正常依赖等待还会安排一分钟后的兜底 reconcile，避免遗漏 watch 事件后永久阻塞。依赖是顺序前提，不是运行时健康输入，因此依赖之后的状态变化不影响已经安装的 Instance。
3. 校验来源，解析 values 和来源凭据，建立唯一的 `install.Instance` 输入。
4. 构造公共 post-render 流水线。
5. 仅当最近一次成功执行仍准确表示当前 generation、values、extensions 和 Artifact 身份时跳过执行；否则通过选中的 installer apply。
6. 读取受管资源并投影运行状态、表达式、scale 观察和 phase。
7. 为所有受管资源种类注册动态 watch。

Apply 失败不会替换最近一次成功执行的结果。失败通过 phase、message 和 conditions 暴露。Reconcile 受运行时选项限制；取消会返回 controller runtime，而不会转换成成功状态。

删除是独立流程。只有选中的 installer 按生命周期策略成功删除或保留受管资源后，finalizer 才会移除。

## 来源与 Values 解析

Artifact 来源引用 Instance 命名空间内的 Secret。Secret 类型不限，但必须 immutable，并包含选中的非空 data key。Instance 和 Secret annotation 中提供的 digest 分别对该数据进行校验。内容校验属于选中的 installer。校验后的字节通过 mode `0600` 的临时文件暴露，文件后缀保留 data key，并在使用后删除。

URL 来源可以解析本地路径、Git 仓库、archive、HTTP Chart 仓库或 OCI Chart。`Downloader.Download` 拥有统一的 singleflight seam，并为每种来源计算仓库 cache base。各来源实现拥有自己的 cache 文件名和复用策略。精确 HTTP Chart version 和 OCI digest 可以在不访问网络的情况下从 cache 返回；version range 和 latest 请求先刷新仓库元数据，再复用已经缓存的解析结果。

HTTP 与 OCI 获取使用项目自有的来源 transport。它 clone Go 默认 HTTP transport，保留环境代理和连接行为，然后应用解析后的 CA bundle、可选客户端证书和 insecure verification 设置。来源 fetcher 拥有 request context、user agent、Bearer 或 basic authentication、redirect credential scope 和 HTTP status 处理。OCI 解析和 layer 获取直接使用 go-containerregistry。直接 zip 与 tar 下载使用同一个 fetcher。Git 使用 go-git transport 接口，并复用同一份解析后的认证和 TLS 材料。

Chart 下载、加载和依赖处理保持为函数，因为每次调用都是操作而非可复用领域对象。底层 `DownloadChart(ctx, destination, fsys, ChartOptions)` 不拥有 cache：它解析来源并写入调用方提供的 filesystem destination。可复用的 `Downloader` 拥有共享 cache base 和并发请求协调，具体来源函数拥有 cache 行为。默认依赖策略是严格模式：每个 Chart 必须已经包含其声明的依赖。Installer 不在运行时修复、build、update 或重写不完整的 Chart。

## Helm adapter 契约

Helm adapter 的安装选项是 `timeout`、`maxHistory`、`disableHooks`、`wait`、`waitForJobs` 和 `subNotes`。Server-side apply 必须关闭；`wait=false` 使用 Helm 的 hook-only 策略，`wait=true` 使用与 Helm 3 兼容的 legacy 策略。这些选择共同定义 Helm release 的等待和提交语义，调用方不得通过未建模的 Helm 参数绕过它们。

资源所有权能力必须与 Installer 的逐资源 Retain/Recreate 生命周期规则共同设计。任何会传播凭据或削弱传输安全的能力都必须成为显式安全策略，不能成为默认行为。Installer 的 Chart、post-render 和 release 接口使用 Chart API v2；切换 Chart API 必须同时重塑这些接口，不能只替换 loader。

Controller 不接纳本地 Helm downloader plugin。新来源协议必须实现为显式的项目来源 adapter，使其凭据、TLS、代理、取消和测试保持确定性。

Values 按以下顺序解析：

1. 按声明顺序引用的 Secret 和 ConfigMap；
2. 内联 `spec.values`；
3. installer 自有运行时 values。

后面的 values 递归覆盖前面的 values，并移除 nil 条目。`spec.replicas` 拥有 `global.replicas`，因此覆盖用户在该路径提供的值。`global.paused` 是独立的用户控制，不从零副本数推导。

## 渲染与平台不变量

Helm、Kustomize 和 Template 模式在资源进入 Kubernetes 前汇合：

1. 渲染选中的来源；
2. 追加从 Chart 派生的 dashboard 资源；
3. 按顺序执行声明的 extensions；
4. 强制执行 namespace 和 scope 权限；
5. 为 Kustomize 和 Template 的直接资源写入 Instance 归属 annotations；Helm 资源使用 Helm release ownership metadata；
6. 应用暂停行为；
7. 在安装 adapter 中校验并转换生命周期策略。

RawManifest 资源与来源渲染资源遵守相同的权限、归属、暂停和生命周期规则。Kustomize 和 Template 模式只在直接资源的顶层 metadata 写入 `apps.xiaoshiai.cn/instance-name` 和 `apps.xiaoshiai.cn/instance-namespace` annotations，不修改 Pod template、selector 或 Chart 自有 labels。这两个 annotation 是 native Instance 归属的保留键；渲染资源中已有的其他 Instance 归属会在安装前被拒绝。Helm 模式由 Helm adapter 写入 release ownership metadata。目标 Pod 的 `app.kubernetes.io/instance` label 属于 Application/Chart 运行时契约，Installer 不从直接资源向 Pod template 推导或覆盖它。

命名空间资源默认进入 Instance namespace。跨 namespace 和 cluster-scoped 资源需要 controller allow-list 或 namespace annotation 授权。授权在 apply 前决定，不委托给各来源模式。

凡是会影响 Helm 渲染 manifest、但不出现在 values 中的输入，都必须纳入 post-render identity。此类输入变化时必须改变 identity version，避免 Helm 错误复用旧 release。

## 运行时配置所有权

应用默认值不能仅为了把同一个值传回二进制而重复成为 Helm value。只有当 Chart 安装过程需要选择该设置或将其与 Kubernetes 资源耦合时，Helm 才暴露设置。

Reconcile timeout 是运行时选项，应用默认值为 15 分钟，并为直接进程调用提供 command flag。Helm Chart 依赖该默认值，不暴露或传递 reconcile timeout。Installer 在内部选择日志配置，因此 Chart 没有 `logLevel` value 或 `LOG_LEVEL` 环境变量契约。网络监听地址、leader election 和 namespace scope 授权仍是 Chart 输入，因为 deployment 或相关 Kubernetes 资源依赖它们。

## 安装与生命周期一致性

Helm 模式拥有一个 Helm release。Kustomize 和 Template 模式渲染普通对象，并使用 native inventory diff。Native adapter 创建新对象，通过 server-side apply 更新现有对象，并删除已经从受管 inventory 消失的对象。所有模式都返回其实际管理的资源。

Lifecycle annotations 在所有模式中含义相同：

- upgrade `Retain` 保持现有资源不变；
- upgrade `Recreate` 使用 foreground propagation 删除变化的资源，等待删除完成后重新创建；
- remove `Retain` 在资源从 manifest 消失或 Instance 被删除时保留该资源。

所有受影响的 lifecycle annotation 都在第一次 mutation 前完成校验。Helm 把 remove retention 映射为自身的 keep policy，并在 Kubernetes adapter 中实现 upgrade policy；native apply 直接执行相同的领域语义。

## Scale、暂停与受管 HPA

`spec.replicas` 是 Instance 期望副本数，并通过 scale subresource 暴露。Controller 将其注入 `global.replicas`；Chart 决定哪个 workload 使用该值，并保证目标 Pod 带有 `app.kubernetes.io/instance=<Instance.name>`。Scale status 统计带该运行时 label 的非终态 Pod，并在存在时叠加 scale Pod selector annotation。Installer 不通过 post-render 修改 Pod template 来建立这个契约。

暂停只由 `global.paused` 控制：

- Deployment 和 StatefulSet 的期望副本数变为零；
- Job 和 CronJob 进入 suspended；
- DaemonSet 获得一个无法满足的 required node affinity；
- 其它资源种类保持不变。

期望副本数为零本身不表示暂停，而是正常、健康的 scaled-to-zero 状态。

受管 HorizontalPodAutoscaler 是由同一个 Instance 渲染和管理生命周期，并以该 Instance 的 Deployment 或 StatefulSet 为目标的 HPA。暂停不会修改或删除 HPA。当 `minReplicas` 大于零时，把目标期望副本数设为零会触发 Kubernetes HPA maintenance-mode deactivation。恢复会重新渲染非零目标，未变化的 HPA 随之重新生效。Installer 不保存 HPA 状态或副本快照。

因此，HPA-managed target 的暂停保证要求 `minReplicas > 0`。支持可能从零开始扩容的 HPA 需要独立的显式设计，因为 Kubernetes 不会对该场景应用 maintenance-mode deactivation。

## 运行时观察

### 事件与状态

受管资源事件通过 `status.resources` 中完整身份的索引返回给所有观察它的 Instance；完整身份包括 GroupVersionKind、namespace 和 name。因此，事件路由不依赖资源 scope、namespace、来源模式或可变 metadata。Watch 按 GroupVersionKind 注册，并使用 metadata-only cache 对象。动态注册的 watch 使用 controller lifecycle context，因此首次发现某资源种类的 reconcile 完成后，该种类之后的事件仍然有效。Pod 还会根据 Application/Chart 提供的运行时 Instance label 被直接 watch，因为 scale 观察包含由受管 workload 创建、但本身不记录在 `status.resources` 中的 Pod。

运行阶段从观察到的 workload states 推导，但显式暂停始终投影为 `Paused`。表达式失败有独立 condition，不覆盖独立计算的运行阶段。默认观察支持常见 workload states，以及 Kubernetes Service、Ingress、LoadBalancer 和 NodePort endpoints；CEL annotations 可以替换 states 或 endpoints，并追加 summary 或 additional endpoints。

Installer 的默认 endpoint 发现只解释其拥有通用语义的 Kubernetes 资源，不识别特定插件或第三方 CR 的 status 结构。创建自定义资源的 Chart 拥有该资源到 Instance endpoint 的映射，并通过 endpoint expression 显式投影；Installer 只提供统一的表达式求值和 endpoint status seam。

`Waiting` 表示当前 generation 已被观察，但执行被预期的外部前提阻塞。依赖等待主要由 dependency Instance watch 唤醒，同时安排一分钟后的重试作为丢失事件的兜底。`Reconciling` 表示安装或更新正在进行，`Failed` 只用于真实的 reconcile 或运行时失败。顶层 status message 描述真实安装或运行时失败；waiting、pending、scaling 和 updating 等预期进度把详情保留在其所属 condition 或 state 上。

Conditions 描述相互独立的保证：依赖、成功安装、运行时就绪、表达式求值和安全 scale 观察。不得从无关 phase 推导 condition。

### 访问端点与节点地址

Endpoint status 是调用方获取 Instance 访问地址的权威接口。Installer 必须按资源语义生成地址：ClusterIP 产生集群内地址，ExternalName 和具有 ingress status 的 LoadBalancer 产生外部地址，Ingress 使用 rule host 和 IngressClass 声明的端口，NodePort 将协议和端口与允许发布的 Node 地址组合。

Node 地址声明遵循以下契约：

- Node label `cloud.xiaoshiai.cn/expose-node-ip=true` 只表达“发布该 Node 的 IP 地址”。
- Node annotation `cloud.xiaoshiai.cn/expose-node-host=<host>` 只表达“发布该 Node 显式声明的 DNS host”。Host 值只包含 URL host，不包含 scheme、port 或 path。
- 两个声明相互独立。Node 可以只发布 Host、只发布 IP，或同时发布两者；没有任何声明的 Node 不参与 NodePort endpoint 解析。
- 只有 `Ready=True` 的 Node 可以贡献地址。IP 来自该 Node `status.addresses` 中的 `InternalIP` 和 `ExternalIP`；Host 来自上述 Annotation。
- Installer 对结果去重并确定性排序，显式 Host 排在自动发现的 IP 前面；IPv6 作为 URL host 时必须使用方括号。
- `Endpoint.url` 保留资源或表达式声明的原始地址，可以包含 `{NodeIP}`；`Endpoint.urls` 只承载模板展开得到的具体地址，并按偏好排序。没有可发布节点地址时保留原始 endpoint，且不虚构 `urls`。
- Installer 不校验 Host 的 DNS 命名规则，而是按 Cloud 声明的值进行地址组合。Cloud 必须保证 Host 满足其集群访问契约。

Cloud 拥有 Node label、annotation 及其生命周期，并负责使声明的 Host/IP 满足集群访问契约；Cloud 可以通过集群接入 adapter 委托 DNS、TLS、路由和 NodePort 可达性的具体配置，但不能把该保证转交给 Installer。Installer 只拥有地址读取、排序和 endpoint 投影。Apps 只投影 installer status，不读取 Node，也不复制地址选择规则。

默认资源发现和 CEL endpoint expression 必须汇合到同一个地址解析路径。Node label、Host annotation、Ready condition 或 Node address 变化必须触发依赖这些地址的 Instance status 重算，使 endpoint 观察最终收敛到 Node 声明。

## 验证规则

行为在最窄的所属 seam 上测试：

- 通过 Instance Kubernetes 接口测试 API 与 scale 行为；
- 通过带真实或本地 installer adapter 的 `InstanceReconciler` 测试 reconcile 保证；
- 通过 `PostRenderer` / `ObjectModifier` 输出测试渲染不变量；
- 通过 Helm 和 native installer 接口测试生命周期语义；
- 通过可观察的 Instance status 测试状态语义。

Instance 资源序列化契约变化时必须重新生成 CRD manifests。平台渲染不变量变化时，其公共渲染测试、所有受影响模式的测试和本文档必须保持一致。
