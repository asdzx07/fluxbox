//go:build ignore

#include <linux/bpf.h>
#include <linux/pkt_cls.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/in.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>

// 内网设备分流策略枚举 (对应前端 modalClientMode: rule=0, direct=1, proxy=2)
#define CLIENT_MODE_RULE   0
#define CLIENT_MODE_DIRECT 1
#define CLIENT_MODE_PROXY  2

// 路由标记：交给 sing-box TProxy 处理的流量标记
#define FWMARK_TPROXY      0x2023
// 路由标记：已放行或 sing-box 自身发出的直连标记
#define FWMARK_BYPASS      0x2024

// LPM Trie 键结构 (用于国内 CIDR 与私有 IP 极速最长前缀匹配)
struct ipv4_lpm_key {
    __u32 prefixlen;
    __u32 addr; // 网络字节序 (Big Endian)
};

// 1. 内网设备分流表: Key = 局域网设备 IPv4 (__be32), Value = 策略 (__u8)
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32);
    __type(value, __u8);
} lan_clients_map SEC(".maps");

// 2. 直连 IP / 国内 CIDR 旁路表 (LPM Trie 树，支持容纳数万条大陆 CIDR 与节点 IP)
struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(max_entries, 65536);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __type(key, struct ipv4_lpm_key);
    __type(value, __u8);
} direct_cidr_map SEC(".maps");

// 3. 全局配置开关 Map: Key 0 = direct_bypass 开关, Key 1 = reject_quic 开关
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 8);
    __type(key, __u32);
    __type(value, __u32);
} global_cfg_map SEC(".maps");

SEC("tc")
int fluxbox_tc_ingress(struct __sk_buff *skb) {
    // 1. 若数据包已带有 sing-box 自身出站标记，直接放行防回环
    if (skb->mark == FWMARK_BYPASS) {
        return TC_ACT_OK;
    }

    void *data     = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;

    // 2. 解析以太网帧头
    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > data_end)
        return TC_ACT_OK;

    if (eth->h_proto != bpf_htons(ETH_P_IP))
        return TC_ACT_OK; // 暂处理 IPv4，IPv6 可同理扩展

    // 3. 解析 IPv4 头
    struct iphdr *iph = (void *)(eth + 1);
    if ((void *)(iph + 1) > data_end)
        return TC_ACT_OK;

    // 仅接管 TCP 与 UDP
    if (iph->protocol != IPPROTO_TCP && iph->protocol != IPPROTO_UDP)
        return TC_ACT_OK;

    __u32 src_ip = iph->saddr;
    __u32 dst_ip = iph->daddr;

    // 4. 【核心特性 A】内网设备分流匹配 (对应面板「内网分流」页)
    __u8 *client_mode = bpf_map_lookup_elem(&lan_clients_map, &src_ip);
    if (client_mode) {
        if (*client_mode == CLIENT_MODE_DIRECT) {
            // 强制直连设备（如 NAS / PT 下载机）：直接内核放行，0 损耗！
            return TC_ACT_OK;
        }
        if (*client_mode == CLIENT_MODE_PROXY) {
            // 强制全局代理设备（如 Apple TV）：跳过国内 IP 绕过检查，直接打标进 sing-box
            skb->mark = FWMARK_TPROXY;
            return TC_ACT_OK;
        }
    }

    // 5. 【核心特性 B】屏蔽 QUIC (UDP 443) 硬件级丢包
    __u32 cfg_quic_key = 1;
    __u32 *reject_quic = bpf_map_lookup_elem(&global_cfg_map, &cfg_quic_key);
    if (reject_quic && *reject_quic == 1 && iph->protocol == IPPROTO_UDP) {
        struct udphdr *udph = (void *)iph + (iph->ihl * 4);
        if ((void *)(udph + 1) <= data_end) {
            if (udph->dest == bpf_htons(443)) {
                return TC_ACT_SHOT; // 内核层直接丢弃 UDP 443，迫使浏览器秒级回退 TCP
            }
        }
    }

    // 6. 【核心特性 C】国内 IP / 节点服务器 IP / 局域网私有 IP 零损耗旁路
    __u32 cfg_bypass_key = 0;
    __u32 *direct_bypass = bpf_map_lookup_elem(&global_cfg_map, &cfg_bypass_key);
    if (!direct_bypass || *direct_bypass == 1) {
        struct ipv4_lpm_key lpm_key = {
            .prefixlen = 32,
            .addr      = dst_ip,
        };
        __u8 *is_direct = bpf_map_lookup_elem(&direct_cidr_map, &lpm_key);
        if (is_direct && *is_direct == 1) {
            // 命中直连 IP 集合：根本不进 sing-box，直接走 Linux 原生路由转发
            return TC_ACT_OK;
        }
    }

    // 7. 其余流量打上 TProxy 路由标记，交由 ip rule (table 2022) 送入 sing-box
    skb->mark = FWMARK_TPROXY;
    return TC_ACT_OK;
}

char __license[] SEC("license") = "GPL";