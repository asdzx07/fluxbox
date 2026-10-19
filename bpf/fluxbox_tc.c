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

#define CLIENT_MODE_RULE   0
#define CLIENT_MODE_DIRECT 1
#define CLIENT_MODE_PROXY  2

#define FWMARK_TPROXY      0x2023
#define FWMARK_BYPASS      0x2024

struct ipv4_lpm_key {
    __u32 prefixlen;
    __u8  addr[4];
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32);
    __type(value, __u8);
} lan_clients_map SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_LPM_TRIE);
    __uint(max_entries, 65536);
    __uint(map_flags, BPF_F_NO_PREALLOC);
    __type(key, struct ipv4_lpm_key);
    __type(value, __u8);
} direct_cidr_map SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 8);
    __type(key, __u32);
    __type(value, __u32);
} global_cfg_map SEC(".maps");

// Key 0: 累计旁路直连包数, Key 1: 累计送入代理包数, Key 2: 累计拦截 QUIC 包数
struct {
    __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
    __uint(max_entries, 4);
    __type(key, __u32);
    __type(value, __u64);
} fluxbox_stats_map SEC(".maps");

static __always_inline void inc_stat(__u32 idx) {
    __u64 *cnt = bpf_map_lookup_elem(&fluxbox_stats_map, &idx);
    if (cnt) {
        *cnt += 1;
    }
}

SEC("tc")
int fluxbox_tc_ingress(struct __sk_buff *skb) {
    // 1. 放行已携带 0x2024 豁免标记的流量
    if (skb->mark == FWMARK_BYPASS) {
        return TC_ACT_OK;
    }

    void *data     = (void *)(long)skb->data;
    void *data_end = (void *)(long)skb->data_end;

    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > data_end)
        return TC_ACT_OK;

    if (eth->h_proto != bpf_htons(ETH_P_IP))
        return TC_ACT_OK;

    struct iphdr *iph = (void *)(eth + 1);
    if ((void *)(iph + 1) > data_end)
        return TC_ACT_OK;

    if (iph->protocol != IPPROTO_TCP && iph->protocol != IPPROTO_UDP)
        return TC_ACT_OK;

    __u32 src_ip = iph->saddr;
    __u32 dst_ip = iph->daddr;

    // 2. 處理 UDP 特例：放行 DHCP (67/68)，接管 DNS (53)
    if (iph->protocol == IPPROTO_UDP) {
        struct udphdr *udph = (void *)iph + (iph->ihl * 4);
        if ((void *)(udph + 1) <= data_end) {
            __u16 dport = bpf_ntohs(udph->dest);
            if (dport == 67 || dport == 68) {
                skb->mark = FWMARK_BYPASS;
                return TC_ACT_OK;
            }
            if (dport == 53) {
                skb->mark = FWMARK_TPROXY;
                return TC_ACT_OK;
            }
        }
    }

    // 3. 局域网私有保留地址互访 (192.168/16, 10/8 等)：打上 BYPASS 免死金牌防止进 TProxy/TUN
    struct ipv4_lpm_key lpm_key = { .prefixlen = 32 };
    __builtin_memcpy(lpm_key.addr, &dst_ip, 4);

    // 4. 内网设备分流匹配 (对应面板「内网分流」页)
    __u8 *client_mode = bpf_map_lookup_elem(&lan_clients_map, &src_ip);
    if (client_mode) {
        if (*client_mode == CLIENT_MODE_DIRECT) {
            // 关键修复：显式打上 0x2024 标记，让后续 ip rule 与 nftables 100% 绕过代理！
            skb->mark = FWMARK_BYPASS;
            inc_stat(0);
            return TC_ACT_OK;
        }
        if (*client_mode == CLIENT_MODE_PROXY) {
            skb->mark = FWMARK_TPROXY;
            inc_stat(1);
            return TC_ACT_OK;
        }
    }

    // 5. 硬件级屏蔽 QUIC (UDP 443)
    __u32 cfg_quic_key = 1;
    __u32 *reject_quic = bpf_map_lookup_elem(&global_cfg_map, &cfg_quic_key);
    if (reject_quic && *reject_quic == 1 && iph->protocol == IPPROTO_UDP) {
        struct udphdr *udph = (void *)iph + (iph->ihl * 4);
        if ((void *)(udph + 1) <= data_end && udph->dest == bpf_htons(443)) {
            inc_stat(2);
            return TC_ACT_SHOT;
        }
    }

    // 6. 国内 CIDR / 节点服务器 IP / 私有网段零损耗旁路
    __u32 cfg_bypass_key = 0;
    __u32 *direct_bypass = bpf_map_lookup_elem(&global_cfg_map, &cfg_bypass_key);
    if (!direct_bypass || *direct_bypass == 1) {
        __u8 *is_direct = bpf_map_lookup_elem(&direct_cidr_map, &lpm_key);
        if (is_direct && *is_direct == 1) {
            // 关键修复：命中直连 IP 集合时打上 0x2024 标记，直接走 main 路由表直连出站！
            skb->mark = FWMARK_BYPASS;
            inc_stat(0);
            return TC_ACT_OK;
        }
    }

    // 7. 其余外网流量打上 0x2023 送入 sing-box
    skb->mark = FWMARK_TPROXY;
    inc_stat(1);
    return TC_ACT_OK;
}

char __license[] SEC("license") = "GPL";