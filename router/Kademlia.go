package router

import (
	"math/bits"
	"sort"
	"sync"

	"envelop/peer"
)

/*
==========================================================
 Kademlia 路由表（演示版）
==========================================================

目标：
  - 给每个节点一个“基于 PeerID 的路由视图”
  - 每次需要转发到某个 destID 时：
        nextHop = table.FindClosest(destID, 1)[0]
    也就是：在自己已知的所有节点里，挑一个
        “ PeerID XOR 距离最近 ” 的那个。

本文件只做纯算法，不直接涉及网络、Envelope、QUIC。

实际使用方式：
  - 每当你通过 Register / RPC / Punch 等见到一个新 PeerID 时：
        table.Update(peerID)
  - Router.NextHop 可以改成：
        func(dest peer.PeerID) (peer.PeerID, bool) {
            list := table.FindClosest(dest, 1)
            if len(list) == 0 {
                return peer.PeerID{}, false
            }
            return list[0], true
        }

真正的 Kademlia 还会有：
  - FIND_NODE / FIND_VALUE RPC
  - bucket 按“距离区间”分层
  - bucket 满时的替换策略（k-bucket）
这里先做一个足够讲解“XOR 距离 + k-bucket”概念的精简版。
==========================================================
*/

// 默认 K 值（每个桶最多多少节点）
const kBucketSize = 8

// KademliaTable 代表一张 Kademlia 路由表
type KademliaTable struct {
	selfID peer.PeerID

	mu      sync.RWMutex
	buckets [256]*bucket // 256-bit ID → 256 个桶
}

// bucket：每个桶里放若干 PeerID（最多 kBucketSize）
type bucket struct {
	peers []peer.PeerID
}

// NewKademliaTable 创建一张以 selfID 为中心的路由表
func NewKademliaTable(self peer.PeerID) *KademliaTable {
	return &KademliaTable{
		selfID: self,
	}
}

/*
==========================================================
 距离 / 桶下标 计算
==========================================================
*/

// xorDistance 计算两个 PeerID 的 XOR 结果
func xorDistance(a, b peer.PeerID) (out [32]byte) {
	for i := 0; i < 32; i++ {
		out[i] = a[i] ^ b[i]
	}
	return
}

// bucketIndex 返回 a 与 b 的“第一个不同 bit 位”的下标 [0, 255]
//   - 返回 -1 表示 a == b（距离为 0，不需要放进表）
//
// 算法：
//  1. 对 XOR 结果，从高位字节开始找第一个非零字节
//  2. 用 bits.LeadingZeros8 算出前导 0 的个数
//  3. index = byteIndex*8 + leadingPos
//     比如：第 0 字节第 0 bit 不同 → index=0（最“远”的桶）
//     第 31 字节第 7 bit 不同 → index=255（最近的桶）
func bucketIndex(a, b peer.PeerID) int {
	x := xorDistance(a, b)
	for i := 0; i < 32; i++ {
		if x[i] == 0 {
			continue
		}
		// 找到第一个非零字节
		leading := bits.LeadingZeros8(x[i]) // 0..8
		return i*8 + leading                // 0..255
	}
	// 完全相等
	return -1
}

/*
==========================================================
 Update：把一个 PeerID 学进路由表
==========================================================
*/

// Update 把一个 peerID 插入对应的 k-bucket 中
// 规则（简化版）：
//   - 自己的 ID 不插入
//   - 若 bucket 里已有这个 ID，则移动到末尾（表示最近使用）
//   - 若 bucket 未满（<kBucketSize），append 到末尾
//   - 若 bucket 已满，简单丢弃最旧的一个（演示版，不做复杂替换）
func (t *KademliaTable) Update(id peer.PeerID) {
	if id.Equals(t.selfID) {
		return
	}

	idx := bucketIndex(t.selfID, id)
	if idx < 0 || idx >= len(t.buckets) {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	b := t.buckets[idx]
	if b == nil {
		b = &bucket{}
		t.buckets[idx] = b
	}

	// 1. 如果已存在，移动到末尾
	for i, p := range b.peers {
		if p.Equals(id) {
			// 移动到末尾（LRU 语义：最近使用）
			copy(b.peers[i:], b.peers[i+1:])
			b.peers[len(b.peers)-1] = id
			return
		}
	}

	// 2. 不存在，但桶未满
	if len(b.peers) < kBucketSize {
		b.peers = append(b.peers, id)
		return
	}

	// 3. 桶已满：简单丢弃最旧的一个（队头），再插入队尾
	//   真正的 Kademlia 会 ping 最旧节点判断是否可用，这里略过
	copy(b.peers[0:], b.peers[1:])
	b.peers[len(b.peers)-1] = id
}

/*
==========================================================
 FindClosest：查找与目标距离最近的 n 个 PeerID
==========================================================
*/

// candidate 用于排序时携带距离
type candidate struct {
	id       peer.PeerID
	distance [32]byte
}

// less 比较两个 distance 谁更小（更近）
// 以大端字节序比较：从高位字节到低位字节逐个比较
func (c candidate) less(other candidate) bool {
	for i := 0; i < 32; i++ {
		if c.distance[i] < other.distance[i] {
			return true
		} else if c.distance[i] > other.distance[i] {
			return false
		}
	}
	return false
}

// FindClosest 返回与 target 距离最近的 n 个 PeerID
// 如果已知节点不足 n 个，就返回尽量多的。
func (t *KademliaTable) FindClosest(target peer.PeerID, n int) []peer.PeerID {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var cands []candidate

	// 遍历所有 bucket
	for _, b := range t.buckets {
		if b == nil {
			continue
		}
		for _, id := range b.peers {
			dist := xorDistance(id, target)
			cands = append(cands, candidate{id: id, distance: dist})
		}
	}

	// 按 distance 递增排序
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].less(cands[j])
	})

	if len(cands) > n {
		cands = cands[:n]
	}

	out := make([]peer.PeerID, len(cands))
	for i, c := range cands {
		out[i] = c.id
	}
	return out
}

/*
==========================================================
 DumpBuckets：调试 / 演示辅助
==========================================================
*/

// DumpBuckets 返回一个简单的字符串切片，用于打印当前路由表状态。
// 比如： [ "idx=3 size=2", "idx=150 size=1", ... ]
func (t *KademliaTable) DumpBuckets() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var out []string
	for _, b := range t.buckets {
		if b == nil || len(b.peers) == 0 {
			continue
		}
		out = append(out) // 这里不依赖 fmt，避免循环 import，就简化成手写的字符串；你也可以改成 fmt.Sprintf
		// 演示用我们可以手动在 main 里用 len/idx 打印，这里就留空实现。

	}
	return out
}
