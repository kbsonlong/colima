# Pod 网络路由自动配置

本文档说明 Colima 中新增的 Pod 网络路由自动配置功能，该功能可以让 macOS 宿主机直接访问 Kubernetes Pod 网络。

## 功能概述

当启用 Kubernetes 和 `network.address` 或网络地址配置时，Colima 会自动：

1. **启动时**：检测 VM IP 和 Pod 网络 CIDR，自动添加路由规则
2. **停止时**：自动清理路由规则，避免残留配置

这使得您可以从 macOS 直接访问 Pod IP 地址，无需手动配置路由。

## 前提条件

1. **macOS 系统**：此功能仅在 macOS 上可用
2. **启用 Kubernetes**：需要在 Colima 配置中启用 Kubernetes
3. **网络配置**：需要启用：
   - `network.address: true`

## 使用方法

### 基本使用

```bash
# 启动 Colima 并启用 Kubernetes 和 network.address
colima start --kubernetes --socket-vmnet

# 或者使用配置文件
colima start --kubernetes
```

### 配置文件示例

在 `~/.colima/default/colima.yaml` 中：

```yaml
kubernetes:
  enabled: true
  version: v1.28.4+k3s1

network:
  
  # 或者启用网络地址
  address: true
```

## 自动路由配置

### 启动时的自动配置

当 Colima 启动时，系统会：

1. 检测 VM 的 IP 地址（通常是 `192.168.105.x` 或 `192.168.5.x`）
2. 获取 Kubernetes 集群的 Pod 网络 CIDR（默认 `10.42.0.0/16`）
3. 自动执行：`sudo route add <POD_CIDR> <VM_IP>`

### 停止时的自动清理

当 Colima 停止时，系统会：

1. 检测当前的 Pod 网络 CIDR
2. 自动执行：`sudo route delete <POD_CIDR>`

## 验证路由配置

### 检查路由表

```bash
# 检查 Pod 网络路由
route -n get 10.42.0.0/16

# 查看完整路由表
netstat -rn | grep 10.42
```

### 测试 Pod 连接

```bash
# 创建测试 Pod
colima ssh -- kubectl run test-pod --image=nginx --port=80

# 获取 Pod IP
POD_IP=$(colima ssh -- kubectl get pod test-pod -o jsonpath='{.status.podIP}')

# 从 macOS 测试连接
curl -I http://$POD_IP

# 清理测试 Pod
colima ssh -- kubectl delete pod test-pod
```

### 使用测试脚本

项目提供了一个测试脚本来验证路由功能：

```bash
# 运行测试脚本
./scripts/test-pod-routing.sh
```

## 日志和调试

### 查看路由配置日志

启动 Colima 时，您会看到类似的日志输出：

```
INFO[0025] Setting up Pod network routing: 10.42.0.0/16 -> 192.168.105.2
INFO[0025] ✅ Pod network route configured successfully: 10.42.0.0/16 -> 192.168.105.2
```

停止 Colima 时：

```
INFO[0001] Cleaning up Pod network routing: 10.42.0.0/16
INFO[0001] ✅ Pod network route cleaned up successfully: 10.42.0.0/16
```

### 调试模式

如果遇到问题，可以启用调试日志：

```bash
# 启用调试日志
export COLIMA_LOG_LEVEL=debug
colima start --kubernetes --socket-vmnet
```

## 故障排除

### 常见问题

1. **路由配置失败**
   ```
   WARN[0025] Failed to setup Pod network routing: failed to add Pod network route
   ```
   
   **解决方案**：
   - 确保有 sudo 权限
   - 检查 VM 是否正常启动
   - 验证网络配置

2. **无法获取 VM IP**
   ```
   WARN[0020] Failed to get VM IP for Pod routing: failed to get VM IP
   ```
   
   **解决方案**：
   - 确保启用了 `network.address`
   - 检查 VM 网络配置
   - 重启 Colima

3. **Pod 无法访问**
   
   **检查步骤**：
   ```bash
   # 1. 检查路由是否存在
   route -n get 10.42.0.0/16
   
   # 2. 检查 VM 是否可达
   ping $(colima ssh -- hostname -I | awk '{print $1}')
   
   # 3. 检查 Pod 是否运行
   colima ssh -- kubectl get pods -A
   
   # 4. 检查 VM 内部路由
   colima ssh -- ip route
   ```

### 手动路由管理

如果自动路由配置失败，您可以手动管理：

```bash
# 手动添加路由
sudo route add 10.42.0.0/16 $(colima ssh -- hostname -I | awk '{print $1}')

# 手动删除路由
sudo route delete 10.42.0.0/16
```

## 安全注意事项

1. **管理员权限**：路由配置需要 sudo 权限
2. **网络隔离**：此配置会使 Pod 网络从宿主机可达，请注意安全影响
3. **防火墙**：确保防火墙配置允许相关流量

## 限制

1. **平台限制**：仅支持 macOS
2. **网络要求**：需要启用 `network.address` 或网络地址配置
3. **权限要求**：需要管理员权限来修改路由表
4. **性能影响**：流量通过 VM 转发可能有轻微性能损失

## 相关文档

- [k3s 网络路由配置](k3s%20网络路由配置.md)
- [Colima 网络配置](../README.md#features)
- [Kubernetes 文档](https://docs.k3s.io/networking)

## 技术实现

路由管理功能位于 `util/routing` 包中，主要组件：

- `RouteManager`：路由规则管理器
- `SetupPodRoutingForProfile()`：启动时配置路由
- `CleanupPodRoutingForProfile()`：停止时清理路由
- `GetVMIP()`：获取 VM IP 地址
- `GetPodCIDR()`：获取 Pod 网络 CIDR

集成点：

- `app/app.go`：在 `Start()` 和 `Stop()` 方法中调用路由管理功能
- 启动后自动配置，停止前自动清理
- 错误不会影响 Colima 的正常启动和停止