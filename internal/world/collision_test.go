package world

import (
	"math"
	"testing"

	"gmcs/internal/registry"
)

// testBox 返回脚底在 (x, y, z) 的玩家尺寸碰撞盒（0.6×1.8）。
func testBox(x, y, z float64) Box {
	return Box{MinX: x - 0.3, MinY: y, MinZ: z - 0.3, MaxX: x + 0.3, MaxY: y + 1.8, MaxZ: z + 0.3}
}

func newTestWorld(t *testing.T) *World {
	t.Helper()
	w, err := Open(t.TempDir(), FlatGenerator{})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// TestCollidesFullBlock 验证整块碰撞与相触判定（相触不算相交）。
func TestCollidesFullBlock(t *testing.T) {
	w := newTestWorld(t)
	// 石头占据 (0,100,0)–(1,101,1)。
	if !w.SetBlock(0, 100, 0, StoneBlock) {
		t.Fatal("SetBlock failed")
	}
	if w.Collides(testBox(0.5, 101, 0.5)) {
		t.Fatal("站在方块顶不应相交")
	}
	if !w.Collides(testBox(0.5, 100.9, 0.5)) {
		t.Fatal("嵌入方块应相交")
	}
	// 相邻格不相交。
	if w.Collides(testBox(2.5, 100.5, 0.5)) {
		t.Fatal("相邻格不应相交")
	}
	// 贴面不算相交：盒子西缘正好在 x=1 平面上。
	if w.Collides(testBox(1.3, 100.5, 0.5)) {
		t.Fatal("贴面不应相交")
	}
	// 头部撞到方块：脚底 99.5 的玩家头部（至 101.3）嵌入 (2,101,0)。
	if !w.SetBlock(2, 101, 0, StoneBlock) {
		t.Fatal("SetBlock failed")
	}
	if !w.Collides(testBox(2.5, 99.5, 0.5)) {
		t.Fatal("头部嵌入方块应相交")
	}
}

// TestCollidesPartialShapes 验证半砖/楼梯/栅栏柱的形状级碰撞。
func TestCollidesPartialShapes(t *testing.T) {
	w := newTestWorld(t)
	// oak_slab bottom=13131：占据 (0,100,0) 下半 [0,0.5]。
	if !w.SetBlock(0, 100, 0, 13131) {
		t.Fatal("SetBlock failed")
	}
	if w.Collides(testBox(0.5, 100.5, 0.5)) {
		t.Fatal("站在半砖顶不应相交")
	}
	if !w.Collides(testBox(0.5, 100.25, 0.5)) {
		t.Fatal("嵌入半砖应相交")
	}
	// 站在半砖旁的地面上（脚 100.0）：头在半砖上方空间，不与半砖相交。
	if w.Collides(testBox(1.5, 100, 0.5)) {
		t.Fatal("站在半砖旁不应相交")
	}
	// 栅栏柱 4×24×4：站在栅栏顶（y=101.5）。
	w2 := newTestWorld(t)
	if !w2.SetBlock(0, 100, 0, registry.BlockStateIDs["minecraft:oak_fence"]) {
		t.Fatal("SetBlock failed")
	}
	if w2.Collides(testBox(0.5, 101.5, 0.5)) {
		t.Fatal("站在栅栏顶不应相交")
	}
	if !w2.Collides(testBox(0.5, 101.4, 0.5)) {
		t.Fatal("嵌入栅栏柱应相交")
	}
	if w2.Collides(testBox(1.5, 100, 0.5)) {
		t.Fatal("站在无连接栅栏旁不应相交")
	}
}

// TestClipMove 验证逐轴扫描裁剪：位移被裁到碰撞面且不穿透。
func TestClipMove(t *testing.T) {
	w := newTestWorld(t)
	if !w.SetBlock(2, 100, 0, StoneBlock) {
		t.Fatal("SetBlock failed")
	}
	b := testBox(0.5, 100, 0.5)
	got := w.ClipMove(b, 0, 5)
	want := 2 - b.MaxX
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("X 裁剪 = %v, want %v", got, want)
	}
	if got = w.ClipMove(testBox(0.5, 102, 0.5), 0, 5); math.Abs(got-5) > 1e-9 {
		t.Fatalf("高位移动不应被裁剪：%v", got)
	}
	// 负方向。
	b2 := testBox(4, 100, 0.5)
	got = w.ClipMove(b2, 0, -5)
	want = 3 - b2.MinX
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("-X 裁剪 = %v, want %v", got, want)
	}
	// Y 轴落地：停在方块顶。
	got = w.ClipMove(testBox(2.5, 103, 0.5), 1, -5)
	want = 101 - 103
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("Y 裁剪 = %v, want %v", got, want)
	}
	// 沿面滑动：盒子东缘贴在墙面上（x=2）沿 Z 移动不应被裁剪。
	b3 := testBox(1.7, 100, 0.5)
	if got = w.ClipMove(b3, 2, 1); math.Abs(got-1) > 1e-9 {
		t.Fatalf("贴面滑动被裁剪：%v", got)
	}
}

// TestSurfaceBelow 验证支撑面检测返回高度与支撑方块。
func TestSurfaceBelow(t *testing.T) {
	w := newTestWorld(t)
	if !w.SetBlock(0, 100, 0, StoneBlock) {
		t.Fatal("SetBlock failed")
	}
	top, state, ok := w.SurfaceBelow(testBox(0.5, 101, 0.5), 0.1)
	if !ok || top != 101 || state != StoneBlock {
		t.Fatalf("整块支撑面 = %v, %v, %v", top, state, ok)
	}
	// 半砖顶面（oak_slab bottom=13131，顶在 +0.5）。
	if !w.SetBlock(0, 100, 0, 13131) {
		t.Fatal("SetBlock failed")
	}
	top, _, ok = w.SurfaceBelow(testBox(0.5, 100.5, 0.5), 0.1)
	if !ok || top != 100.5 {
		t.Fatalf("半砖支撑面 = %v, %v", top, ok)
	}
	// 栅栏顶面 1.5（原版碰撞高度）。
	w2 := newTestWorld(t)
	if !w2.SetBlock(0, 100, 0, registry.BlockStateIDs["minecraft:oak_fence"]) {
		t.Fatal("SetBlock failed")
	}
	top, _, ok = w2.SurfaceBelow(testBox(0.5, 101.5, 0.5), 0.1)
	if !ok || top != 101.5 {
		t.Fatalf("栅栏支撑面 = %v, %v", top, ok)
	}
	// gap 不足时找不到支撑面。
	if _, _, ok = w2.SurfaceBelow(testBox(0.5, 102, 0.5), 0.1); ok {
		t.Fatal("悬空不应有支撑面")
	}
}

// BenchmarkCollides 基准：玩家碰撞盒在典型地形上的查询。
func BenchmarkCollides(b *testing.B) {
	w, err := Open(b.TempDir(), FlatGenerator{})
	if err != nil {
		b.Fatal(err)
	}
	box := testBox(0.5, 100.5, 0.5)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = w.Collides(box)
	}
}

// BenchmarkClipMove 基准：逐轴扫描裁剪。
func BenchmarkClipMove(b *testing.B) {
	w, err := Open(b.TempDir(), FlatGenerator{})
	if err != nil {
		b.Fatal(err)
	}
	box := testBox(0.5, 100.5, 0.5)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = w.ClipMove(box, 0, 0.2)
	}
}
