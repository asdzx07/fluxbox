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

// 修复：使用 __u8 addr[4] 严格保证网络字节序，避免 LittleEndian 导致 LPM 树匹配失效
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

SEC("tc")
int fluxbox_tc_ingress(struct __sk_buff *skb) {
    // 1. 放行 sing-box 自身发出的流量 (0x2024)
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

    // 2. 放行 DHCP (UDP 67/68) 广播包，防止局域网设备获取不到 IP
    if (iph->protocol == IPPROTO_UDP) {
        struct udphdr *udph = (void *)iph + (iph->ihl * 4);
        if ((void *)(udph + 1) <= data_end) {
            __u16 dport = bpf_ntohs(udph->dest);
            if (dport == 67 || dport == 68) {
                return TC_ACT_OK;
            }
            // 如果是 DNS 查询 (UDP 53)，强制打上 TProxy 标记交由 sing-box hijack-dns 处理！
            if (dport == 53) {
                skb->mark = FWMARK_TPROXY;
                return TC_ACT_OK;
            }
        }
    }

    // 3. 内网设备分流匹配
    __u8 *client_mode = bpf_map_lookup_elem(&lan_clients_map, &src_ip);
    if (client_mode) {
        if (*client_mode == CLIENT_MODE_DIRECT) {
            return TC_ACT_OK;
        }
        if (*client_mode == CLIENT_MODE_PROXY) {
            skb->mark = FWMARK_TPROXY;
            return TC_ACT_OK;
        }
    }

    // 4. 屏蔽 QUIC (UDP 443)
    __u32 cfg_quic_key = 1;
    __u32 *reject_quic = bpf_map_lookup_elem(&global_cfg_map, &cfg_quic_key);
    if (reject_quic && *reject_quic == 1 && iph->protocol == IPPROTO_UDP) {
        struct udphdr *udph = (void *)iph + (iph->ihl * 4);
        if ((void *)(udph + 1) <= data_end && udph->dest == bpf_htons(443)) {
            return TC_ACT_SHOT;
        }
    }

    // 5. 局域网保留网段与国内 CIDR 直连旁路检查
    struct ipv4_lpm_key lpm_key = { .prefixlen = 32 };
    __builtin_memcpy(lpm_key.addr, &dst_ip, 4);

    __u8 *is_direct = bpf_map_lookup_elem(&direct_cidr_map, &lpm_key);
    if (is_direct && *is_direct == 1) {
        return TC_ACT_OK;
    }

    // 6. 其余外网流量打标 0x2023 送入 sing-box TProxy
    skb->mark = FWMARK_TPROXY;
    return TC_ACT_OK;
}

char __license[] SEC("license") = "GPL";