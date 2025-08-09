# Pod 网络路由配置

本文档说明如何在 macOS 上配置路由规则，使宿主机能够直接访问 Kubernetes Pod 网络。

## 问题描述

当在 Colima 中运行 Kubernetes 时，Pod 使用的网络 CIDR 默认为 `10.42.0.0/16`（由 k3s 的 flannel CNI 配置）。macOS 宿主机无法直接访问这个网络，因为它只能访问 VM 的 IP 地址（如 `192.168.105.2`）。

## 解决方案

通过在 macOS 上添加路由规则，将 Pod 网络流量转发到 Colima VM，然后由 VM 转发到具体的 Pod。

### 前提条件

1. Colima 已启动并启用了 Kubernetes
2. 使用 `--network-address` 选项，确保 VM 有可达的 IP 地址
3. 确认 VM 的 IP 地址（通常是 `192.168.105.2` 或 `192.168.5.x`）

### 自动配置脚本

创建以下脚本来自动配置路由：

```bash
#!/bin/bash
# setup-pod-routing.sh

set -e

# 获取 Colima VM 的 IP 地址
VM_IP=$(colima ssh -- ip route get 1.1.1.1 | grep -oP 'src \K[\d.]+')
if [ -z "$VM_IP" ]; then
    echo "错误: 无法获取 VM IP 地址，请确保 Colima 正在运行"
    exit 1
fi

echo "检测到 VM IP: $VM_IP"

# 检查是否启用了 Kubernetes
if ! colima ssh -- kubectl cluster-info &>/dev/null; then
    echo "错误: Kubernetes 未启用或未运行"
    exit 1
fi

# 获取 Pod 网络 CIDR
POD_CIDR=$(colima ssh -- kubectl cluster-info dump | grep -oP 'cluster-cidr=\K[\d./]+' | head -1)
if [ -z "$POD_CIDR" ]; then
    # 使用默认的 k3s Pod CIDR
    POD_CIDR="10.42.0.0/16"
fi

echo "Pod 网络 CIDR: $POD_CIDR"

# 检查现有路由
if route -n get "$POD_CIDR" &>/dev/null; then
    echo "路由已存在，删除旧路由..."
    sudo route delete "$POD_CIDR" &>/dev/null || true
fi

# 添加新路由
echo "添加路由: $POD_CIDR -> $VM_IP"
sudo route add "$POD_CIDR" "$VM_IP"

# 验证路由
if route -n get "$POD_CIDR" | grep -q "$VM_IP"; then
    echo "✅ 路由配置成功"
    echo "现在可以从 macOS 直接访问 Pod IP 地址"
else
    echo "❌ 路由配置失败"
    exit 1
fi

# 显示路由信息
echo "\n当前路由信息:"
route -n get "$POD_CIDR"
```

### 手动配置

如果不想使用脚本，可以手动执行以下步骤：

1. **获取 VM IP 地址**：
   ```bash
   # 方法1: 通过 colima status
   colima status
   
   # 方法2: 通过 SSH 获取
   colima ssh -- hostname -I
   ```

2. **获取 Pod 网络 CIDR**：
   ```bash
   # 查看 k3s 集群信息
   colima ssh -- kubectl cluster-info dump | grep cluster-cidr
   
   # 或查看 flannel 配置
   colima ssh -- cat /var/lib/rancher/k3s/server/manifests/flannel.yaml
   ```

3. **添加路由规则**：
   ```bash
   # 假设 VM IP 是 192.168.105.2，Pod CIDR 是 10.42.0.0/16
   sudo route add 10.42.0.0/16 192.168.105.2
   ```

### 验证配置

1. **检查路由表**：
   ```bash
   route -n get 10.42.0.0/16
   ```

2. **测试 Pod 连接**：
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

### 持久化配置

由于 macOS 重启后路由规则会丢失，可以创建一个启动脚本：

1. **创建 LaunchDaemon**：
   ```bash
   sudo tee /Library/LaunchDaemons/com.colima.pod-routing.plist > /dev/null <<EOF
   <?xml version="1.0" encoding="UTF-8"?>
   <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
   <plist version="1.0">
   <dict>
       <key>Label</key>
       <string>com.colima.pod-routing</string>
       <key>ProgramArguments</key>
       <array>
           <string>/usr/local/bin/setup-pod-routing.sh</string>
       </array>
       <key>RunAtLoad</key>
       <true/>
       <key>KeepAlive</key>
       <false/>
   </dict>
   </plist>
   EOF
   ```

2. **加载 LaunchDaemon**：
   ```bash
   sudo launchctl load /Library/LaunchDaemons/com.colima.pod-routing.plist
   ```

### 清理路由

如果需要删除路由规则：

```bash
#!/bin/bash
# cleanup-pod-routing.sh

# 删除 Pod 网络路由
sudo route delete 10.42.0.0/16 &>/dev/null || true

# 卸载 LaunchDaemon（如果存在）
sudo launchctl unload /Library/LaunchDaemons/com.colima.pod-routing.plist &>/dev/null || true
sudo rm -f /Library/LaunchDaemons/com.colima.pod-routing.plist

echo "Pod 网络路由已清理"
```

### 故障排除

1. **路由不生效**：
   - 确认 VM IP 地址正确
   - 检查 VM 是否启用了 IP 转发：`colima ssh -- sysctl net.ipv4.ip_forward`
   - 确认防火墙设置

2. **Pod 无法访问**：
   - 检查 Pod 是否正在运行
   - 确认 Pod 的网络策略
   - 检查 Service 配置

3. **性能问题**：
   - 路由通过 VM 转发可能会有轻微的性能损失
   - 考虑使用 Service 的 NodePort 或 LoadBalancer 类型

### 注意事项

- 此配置需要管理员权限
- 路由规则在系统重启后会丢失，需要重新配置
- 如果 VM IP 地址发生变化，需要更新路由规则
- 建议在开发环境中使用，生产环境应使用更稳定的网络解决方案

### 相关文档

- [Colima 网络配置](../README.md#features)
- [k3s 网络文档](https://docs.k3s.io/networking)
