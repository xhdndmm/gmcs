//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

package server

import (
	"context"
	"math"
	"net"
	"testing"
	"time"

	"gmcs/internal/config"
	"gmcs/internal/protocol"
	"gmcs/internal/registry"
	"gmcs/internal/world"
)

// 天气系统测试：
//   - 周期推进（雨/雷计时、强度 0.01 步进与 clamp、翻转时广播事件 1/2 + 7/8）；
//   - /weather clear 的强制放晴倒计时与到期行为；
//   - 进入世界时同步当前天气；
//   - 降雨对耕地的滋润、对火焰与实体的浇灭；
//   - 天空暴露判定（有碰撞形状的方块阻挡、火焰不阻挡）；
//   - 关闭天气时不推进。

// decodeGameEvent 解析 Game Event 包（类型 u8 + 数值 f32）。
func decodeGameEvent(t *testing.T, payload []byte) (uint8, float32) {
	t.Helper()
	_, offset, err := protocol.DecodeVarInt(payload) // 包 ID
	if err != nil {
		t.Fatalf("解析包 ID：%v", err)
	}
	if offset+5 > len(payload) {
		t.Fatalf("Game Event 包过短：%d 字节", len(payload))
	}
	event := payload[offset]
	bits := uint32(payload[offset+1])<<24 | uint32(payload[offset+2])<<16 |
		uint32(payload[offset+3])<<8 | uint32(payload[offset+4])
	return event, math.Float32frombits(bits)
}

// TestWeatherCycleTogglesRain 验证雨计时归零时翻转下雨状态并按原版区间重掷计时。
func TestWeatherCycleTogglesRain(t *testing.T) {
	instance := newHopperTestServer(t)
	instance.weatherEnabled = true

	// 雨计时 1 tick 后自动翻转：不下雨 → 下雨（雷计时单独设置，避免同时翻转）。
	instance.weatherMu.Lock()
	instance.weather = weatherState{rainTicks: 1, thunderTicks: 100}
	instance.weatherMu.Unlock()
	instance.tickWeather(nil)
	if !instance.isRaining() {
		t.Fatal("雨计时归零后应开始下雨")
	}
	if level := instance.weatherSnapshot().rainLevel; math.Abs(float64(level-0.01)) > 1e-6 {
		t.Fatalf("下雨 1 tick 后 rainLevel = %v, want 0.01", level)
	}
	// 翻转后的下一 tick：雨计时为 0，按“正在下雨”区间（3600–15600）重掷。
	instance.tickWeather(nil)
	state := instance.weatherSnapshot()
	if state.rainTicks < weatherRainTimeRainingMin || state.rainTicks > weatherRainTimeRainingMax {
		t.Fatalf("雨中计时 = %d, want %d..%d",
			state.rainTicks, weatherRainTimeRainingMin, weatherRainTimeRainingMax)
	}
	// 雨计时到期后（thunderTicks 归零）按“未打雷”区间（12000–180000）重掷。
	instance.weatherMu.Lock()
	instance.weather = weatherState{thunderTicks: 1}
	instance.weatherMu.Unlock()
	instance.tickWeather(nil)
	if !instance.isThundering() {
		t.Fatal("雷计时归零后应开始打雷")
	}
	instance.tickWeather(nil)
	if ticks := instance.weatherSnapshot().thunderTicks; ticks < weatherThunderTimeThunderingMin || ticks > weatherThunderTimeThunderingMax {
		t.Fatalf("雷暴计时 = %d, want %d..%d",
			ticks, weatherThunderTimeThunderingMin, weatherThunderTimeThunderingMax)
	}
	// 继续 200 tick：rainLevel 应被 clamp 到 1。
	instance.weatherMu.Lock()
	instance.weather = weatherState{raining: true, rainTicks: 10000}
	instance.weatherMu.Unlock()
	for i := 0; i < 200; i++ {
		instance.tickWeather(nil)
	}
	if level := instance.weatherSnapshot().rainLevel; level != 1 {
		t.Fatalf("rainLevel 未 clamp 到 1：%v", level)
	}
}

// TestWeatherClearCountdownRestartsRain 验证 /weather clear 的倒计时：
// 倒计时期间强制晴朗且不推进强度，倒计时结束后按原版字节码行为重新开始下雨。
func TestWeatherClearCountdownRestartsRain(t *testing.T) {
	instance := newHopperTestServer(t)
	instance.weatherEnabled = true
	instance.setWeatherParameters(3, 0, true, true)

	for i := 0; i < 3; i++ {
		instance.tickWeather(nil)
		if instance.isRaining() || instance.isThundering() {
			t.Fatalf("强制放晴第 %d tick 天气未清零", i+1)
		}
	}
	state := instance.weatherSnapshot()
	if state.clearTicks != 0 {
		t.Fatalf("放晴倒计时 = %d, want 0", state.clearTicks)
	}
	// 原版：倒计时结束时 rainTime 被压到 1，下一 tick 归零即翻转为下雨。
	instance.tickWeather(nil)
	if !instance.isRaining() {
		t.Fatal("强制放晴结束后应重新开始下雨（原版 advanceWeatherCycle 行为）")
	}
}

// TestWeatherBroadcastTransition 验证下雨翻转时向主世界玩家广播事件 1（开始下雨）。
func TestWeatherBroadcastTransition(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "RainWatch")
	defer conn.Close()
	instance.weatherEnabled = true

	instance.setWeatherParameters(0, 1, false, false)
	instance.tick()
	event, value := decodeGameEvent(t, expectPlayPacket(t, conn, protocol.PlayPacketIDGameEvent))
	if event != gameEventStartRaining || value != 0 {
		t.Fatalf("开始下雨事件 = %d/%v, want %d/0", event, value, gameEventStartRaining)
	}
	// 随后补发雷/雨强度（先 8 后 7）。
	thunderEvent, _ := decodeGameEvent(t, expectPlayPacket(t, conn, protocol.PlayPacketIDGameEvent))
	if thunderEvent != gameEventThunderLevel {
		t.Fatalf("补发事件顺序错误：want %d, got %d", gameEventThunderLevel, thunderEvent)
	}
	rainEvent, _ := decodeGameEvent(t, expectPlayPacket(t, conn, protocol.PlayPacketIDGameEvent))
	if rainEvent != gameEventRainLevel {
		t.Fatalf("补发事件顺序错误：want %d, got %d", gameEventRainLevel, rainEvent)
	}
}

// TestWeatherJoinSync 验证中途加入的玩家在进入世界时收到当前天气
// （原版只在变化时广播，gmcs 额外在加入时同步，见 weatherJoinPackets）。
func TestWeatherJoinSync(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.ViewDistance = 2
	instance, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	instance.keepAliveInterval = time.Hour
	instance.tickInterval = 0
	instance.chunkUnloadInterval = time.Hour
	instance.chunkUnloadGrace = 0
	instance.dimensionIdleGrace = time.Hour
	instance.playerAutosaveInterval = time.Hour
	defer func() { _ = instance.worldFor(world.DimensionOverworld).Close() }()

	// 先让天气处于“雨中且强度已满”，再让玩家登录。
	instance.setWeatherParameters(0, 1000, true, true)
	for i := 0; i < 100; i++ {
		instance.tickWeather(nil)
	}
	if !instance.isRaining() {
		t.Fatal("测试前置：应处于下雨状态")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveResult := make(chan error, 1)
	go func() { serveResult <- instance.Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-serveResult:
			if err != nil {
				t.Errorf("Serve() after cancellation: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve() did not stop after context cancellation")
		}
	})

	var events []uint8
	conn := joinAt(t, cfg, listener.Addr().String(), "RainJoiner",
		func(id int32, payload []byte) {
			if id == protocol.PlayPacketIDGameEvent {
				event, _ := decodeGameEvent(t, payload)
				events = append(events, event)
			}
		})
	defer conn.Close()
	// 加入流程里已经有 Game Event 13（等待区块），因此按“包含事件 1”判定。
	foundStart := false
	for _, event := range events {
		if event == gameEventStartRaining {
			foundStart = true
			break
		}
	}
	if !foundStart {
		t.Fatalf("加入时未同步下雨状态：events=%v", events)
	}
	foundThunder, foundRain := false, false
	for _, event := range events {
		switch event {
		case gameEventThunderLevel:
			foundThunder = true
		case gameEventRainLevel:
			foundRain = true
		}
	}
	if !foundThunder || !foundRain {
		t.Fatalf("加入时未同步天气强度：events=%v", events)
	}
}

// TestWeatherRainHydratesFarmland 验证下雨时耕地随机刻直接把湿度设为 7
// （原版 FarmBlock.randomTick 的 isRainingAt(pos.above()) 分支）。
func TestWeatherRainHydratesFarmland(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 60, 60
	groundY := hopperTestPad(t, instance, x, z, 4)
	w := instance.testWorld()
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	// 铺一块没有水的地面：湿度只能靠降雨回升。
	for dx := -6; dx <= 6; dx++ {
		for dz := -6; dz <= 6; dz++ {
			w.SetBlock(x+dx, groundY, z+dz, stone)
			w.SetBlock(x+dx, groundY+1, z+dz, world.AirBlock)
		}
	}
	setFarmland(t, instance, x, groundY, z, 0)
	instance.registerFarmland(world.DimensionOverworld, x, groundY, z)

	// 未下雨：湿度应逐级衰减而不是回升（随机刻 3/4096，取足够多的 tick）。
	instance.setWeatherParameters(0, 1000, false, false)
	setFarmland(t, instance, x, groundY, z, 5)
	for i := 0; i < 20000; i++ {
		instance.tickCrops()
	}
	if moisture := farmlandAt(instance, x, groundY, z); moisture >= 5 {
		t.Fatalf("未下雨时湿度不应回升（初始 5）：moisture=%d", moisture)
	}

	setFarmland(t, instance, x, groundY, z, 0)
	// 相位 A 中湿度耗尽后退化为泥土时会从随机刻表注销，这里重新登记。
	instance.registerFarmland(world.DimensionOverworld, x, groundY, z)
	instance.setWeatherParameters(0, 1000, true, false)

	instance.setWeatherParameters(0, 1000, true, false)
	for i := 0; i < 200000; i++ {
		instance.tickCrops()
		if farmlandAt(instance, x, groundY, z) == 7 {
			return
		}
	}
	t.Fatalf("下雨时耕地未升至湿度 7（当前 moisture=%d）", farmlandAt(instance, x, groundY, z))
}

// TestWeatherExtinguishesFire 验证雨天浇灭火焰（原版 FireBlock.tick：
// 0.2 + age × 0.03 的概率），且不下雨时火不会因此熄灭。
func TestWeatherExtinguishesFire(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 80, 80
	groundY := hopperTestPad(t, instance, x, z, 2)
	w := instance.testWorld()
	netherrack, ok := registry.BlockStateIDs["minecraft:netherrack"]
	if !ok {
		t.Fatal("缺少下界岩方块状态")
	}
	fireState, ok := registry.BlockStateIDs["minecraft:fire"]
	if !ok {
		t.Fatal("缺少火焰方块状态")
	}
	// 下界岩属于 infiniburn 标签：火焰不会因为“无燃料”自行熄灭，
	// 从而孤立出降雨这一个变量。
	w.SetBlock(x, groundY, z, netherrack)
	w.SetBlock(x, groundY+1, z, world.AirBlock)
	pos := [3]int{x, groundY + 1, z}
	_, props, ok := registry.StateProps(fireState)
	if !ok {
		t.Fatal("缺少火焰方块状态属性")
	}

	w.SetBlock(pos[0], pos[1], pos[2], fireState)
	instance.setWeatherParameters(0, 1000, false, false)
	for i := 0; i < 20; i++ {
		instance.fireTickBody(w, world.DimensionOverworld, pos, props)
	}
	if !registry.IsFireState(w.BlockAt(pos[0], pos[1], pos[2])) {
		t.Fatal("未下雨时火焰不应被浇灭")
	}

	// 下雨：重新放置 age=0 的火焰，最多 40 次推进内必被浇灭。
	w.SetBlock(pos[0], pos[1], pos[2], fireState)
	instance.setWeatherParameters(0, 1000, true, false)
	extinguished := false
	for i := 0; i < 40; i++ {
		if !registry.IsFireState(w.BlockAt(pos[0], pos[1], pos[2])) {
			extinguished = true
			break
		}
		instance.fireTickBody(w, world.DimensionOverworld, pos, props)
	}
	if !extinguished {
		t.Fatal("下雨时火焰未被浇灭")
	}
}

// TestWeatherExtinguishesPlayerFire 验证雨中玩家的着火被清除，而有遮挡时不会。
func TestWeatherExtinguishesPlayerFire(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	instance, conn := joinServer(t, cfg, "RainFire")
	defer conn.Close()
	player := findSession(t, instance, "RainFire")
	processBarrier(t, conn)

	x, y, z, _, _ := player.playerPosition()
	bx, by, bz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	clearPlayerArea(instance, bx, by-1, bz, 1, 24)

	instance.setWeatherParameters(0, 1000, true, false)
	player.fireTicks.Store(20)
	instance.tick()
	if ticks := player.fireTicks.Load(); ticks != 0 {
		t.Fatalf("露天雨中着火未清除：fireTicks = %d", ticks)
	}

	// 头顶加一块石头遮挡 → 不再被雨浇灭（着火只递减）。
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	if !instance.testWorld().SetBlock(bx, by+2, bz, stone) {
		t.Fatal("无法放置遮挡方块")
	}
	player.fireTicks.Store(20)
	instance.tick()
	if ticks := player.fireTicks.Load(); ticks == 0 {
		t.Fatal("有遮挡时不应被雨浇灭")
	}
}

// TestWeatherCommand 验证 /weather 的参数解析与状态写入。
func TestWeatherCommand(t *testing.T) {
	cfg := config.Default()
	cfg.WorldDir = t.TempDir()
	cfg.SpawnMonsters = false
	cfg.Ops = []string{"WeatherOp"}
	instance, conn := joinServer(t, cfg, "WeatherOp")
	defer conn.Close()
	processBarrier(t, conn)

	sendChatCommand(t, conn, "/weather rain 60")
	expectSystemChat(t, conn, "已将天气设置为 rain")
	state := instance.weatherSnapshot()
	if !state.raining || state.thundering {
		t.Fatalf("rain 之后状态 = raining %v/thundering %v", state.raining, state.thundering)
	}
	if state.rainTicks != 60*20 || state.thunderTicks != 60*20 {
		t.Fatalf("rain 60 秒计时 = %d/%d, want 1200/1200", state.rainTicks, state.thunderTicks)
	}

	sendChatCommand(t, conn, "/weather thunder")
	expectSystemChat(t, conn, "已将天气设置为 thunder")
	state = instance.weatherSnapshot()
	if !state.raining || !state.thundering {
		t.Fatalf("thunder 之后状态 = raining %v/thundering %v", state.raining, state.thundering)
	}
	if state.rainTicks < weatherThunderTimeThunderingMin || state.rainTicks > weatherThunderTimeThunderingMax {
		t.Fatalf("thunder 默认时长 = %d, want %d..%d",
			state.rainTicks, weatherThunderTimeThunderingMin, weatherThunderTimeThunderingMax)
	}

	sendChatCommand(t, conn, "/weather clear 30")
	expectSystemChat(t, conn, "已将天气设置为 clear")
	state = instance.weatherSnapshot()
	if state.raining || state.thundering {
		t.Fatalf("clear 之后状态 = raining %v/thundering %v", state.raining, state.thundering)
	}
	if state.clearTicks != 30*20 {
		t.Fatalf("clear 30 秒倒计时 = %d, want 600", state.clearTicks)
	}

	sendChatCommand(t, conn, "/weather snow")
	expectSystemChat(t, conn, "未知天气类型：snow")
}

// TestWeatherSkyExposure 验证天空暴露判定：有碰撞形状的方块阻挡降水，
// 火焰/空气不阻挡。
func TestWeatherSkyExposure(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 100, 100
	groundY := hopperTestPad(t, instance, x, z, 2)
	w := instance.testWorld()
	stone, ok := registry.BlockStateIDs["minecraft:stone"]
	if !ok {
		t.Fatal("缺少石头方块状态")
	}
	fireState, ok := registry.BlockStateIDs["minecraft:fire"]
	if !ok {
		t.Fatal("缺少火焰方块状态")
	}
	for dy := 1; dy <= 6; dy++ {
		w.SetBlock(x, groundY+dy, z, world.AirBlock)
	}
	if !skyExposedAt(w, x, groundY+1, z) {
		t.Fatal("露天位置应暴露于天空")
	}
	w.SetBlock(x, groundY+4, z, stone)
	if skyExposedAt(w, x, groundY+1, z) {
		t.Fatal("上方有石头时不应暴露于天空")
	}
	// 火焰没有碰撞形状，不阻挡降水（原版 Heightmap.MOTION_BLOCKING）。
	w.SetBlock(x, groundY+4, z, fireState)
	if !skyExposedAt(w, x, groundY+1, z) {
		t.Fatal("上方只有火焰时应仍暴露于天空")
	}
	if instance.isRainingAt(world.DimensionOverworld, x, groundY+1, z) {
		t.Fatal("未下雨时 isRainingAt 必须为 false")
	}
}

// TestWeatherRainBlocksIgnition 验证雨天不会把旁边的可燃方块点燃，而是直接烧毁
// （原版 FireBlock.checkBurnOut：`nextInt(age+10) < 5 && !isRainingAt(pos)`）。
func TestWeatherRainBlocksIgnition(t *testing.T) {
	instance := newHopperTestServer(t)
	x, z := 120, 120
	groundY := hopperTestPad(t, instance, x, z, 2)
	w := instance.testWorld()
	planks, ok := registry.BlockStateIDs["minecraft:oak_planks"]
	if !ok {
		t.Fatal("缺少木板方块状态")
	}
	target := [3]int{x + 1, groundY + 1, z}
	w.SetBlock(target[0], target[1], target[2], planks)
	instance.setWeatherParameters(0, 1000, true, false)

	for i := 0; i < 20000; i++ {
		state := w.BlockAt(target[0], target[1], target[2])
		if state != planks {
			if registry.IsFireState(state) {
				t.Fatal("雨天不应把可燃方块点燃")
			}
			return
		}
		instance.fireCheckBurnOut(w, world.DimensionOverworld, target, 300, 0)
	}
	t.Fatal("雨天燃烧判定 20000 次后目标方块仍未变化")
}

// TestWeatherDisabled 验证 weatherEnabled=false 时天气不推进。
func TestWeatherDisabled(t *testing.T) {
	instance := newHopperTestServer(t) // 已关闭天气
	for i := 0; i < 500; i++ {
		instance.tickWeather(nil)
	}
	state := instance.weatherSnapshot()
	if state.raining || state.thundering || state.rainLevel != 0 || state.thunderLevel != 0 {
		t.Fatalf("关闭天气后状态应保持晴朗：%+v", state)
	}
	if packets := instance.weatherJoinPackets(); packets != nil {
		t.Fatalf("关闭天气时不应发送加入同步包：%d 个", len(packets))
	}
}
