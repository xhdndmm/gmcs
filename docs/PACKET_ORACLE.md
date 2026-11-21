# Clientbound 包覆盖清单（1.21.11 / 协议 774）

本表由官方数据生成器报告 `generated/reports/packets.json` 的 `protocol_id` 生成，
覆盖 **Configuration（20）** 与 **Play（139）** 阶段的全部 clientbound 包。
“编码函数”列列出 gmcs 当前实际用于该包的函数；`—` 表示尚未实现发包路径，
不代表客户端不能接收该包。`world.*` 表示编码函数位于 `internal/world`。

## Configuration（20 个）

| ID | 官方包名 | gmcs 编码函数 |
| ---: | --- | --- |
| 0 | `minecraft:cookie_request` | — |
| 1 | `minecraft:custom_payload` | `EncodeBrand` |
| 2 | `minecraft:disconnect` | — |
| 3 | `minecraft:finish_configuration` | `EncodeFinishConfiguration` |
| 4 | `minecraft:keep_alive` | — |
| 5 | `minecraft:ping` | — |
| 6 | `minecraft:reset_chat` | — |
| 7 | `minecraft:registry_data` | `EncodeRegistryData` |
| 8 | `minecraft:resource_pack_pop` | — |
| 9 | `minecraft:resource_pack_push` | — |
| 10 | `minecraft:store_cookie` | — |
| 11 | `minecraft:transfer` | — |
| 12 | `minecraft:update_enabled_features` | `EncodeFeatureFlags` |
| 13 | `minecraft:update_tags` | `EncodeUpdateTags` |
| 14 | `minecraft:select_known_packs` | `EncodeKnownPacks` |
| 15 | `minecraft:custom_report_details` | — |
| 16 | `minecraft:server_links` | — |
| 17 | `minecraft:clear_dialog` | — |
| 18 | `minecraft:show_dialog` | — |
| 19 | `minecraft:code_of_conduct` | — |

## Play（139 个）

| ID | 官方包名 | gmcs 编码函数 |
| ---: | --- | --- |
| 0 | `minecraft:bundle_delimiter` | — |
| 1 | `minecraft:add_entity` | `EncodeAddEntity` |
| 2 | `minecraft:animate` | `EncodeAnimate` |
| 3 | `minecraft:award_stats` | — |
| 4 | `minecraft:block_changed_ack` | `EncodeBlockChangedAck` |
| 5 | `minecraft:block_destruction` | `EncodeBlockDestruction` |
| 6 | `minecraft:block_entity_data` | — |
| 7 | `minecraft:block_event` | `EncodeBlockAction` |
| 8 | `minecraft:block_update` | `EncodeBlockUpdate` |
| 9 | `minecraft:boss_event` | — |
| 10 | `minecraft:change_difficulty` | `EncodeChangeDifficulty` |
| 11 | `minecraft:chunk_batch_finished` | `EncodeChunkBatchFinished` |
| 12 | `minecraft:chunk_batch_start` | `EncodeChunkBatchStart` |
| 13 | `minecraft:chunks_biomes` | — |
| 14 | `minecraft:clear_titles` | — |
| 15 | `minecraft:command_suggestions` | — |
| 16 | `minecraft:commands` | `EncodeDeclareCommands` |
| 17 | `minecraft:container_close` | `EncodeContainerClose` |
| 18 | `minecraft:container_set_content` | `EncodeContainerSetContent` |
| 19 | `minecraft:container_set_data` | `EncodeSetContainerProperty` |
| 20 | `minecraft:container_set_slot` | `EncodeSetContainerSlot` |
| 21 | `minecraft:cookie_request` | — |
| 22 | `minecraft:cooldown` | — |
| 23 | `minecraft:custom_chat_completions` | — |
| 24 | `minecraft:custom_payload` | — |
| 25 | `minecraft:damage_event` | `EncodeDamageEvent` |
| 26 | `minecraft:debug/block_value` | — |
| 27 | `minecraft:debug/chunk_value` | — |
| 28 | `minecraft:debug/entity_value` | — |
| 29 | `minecraft:debug/event` | — |
| 30 | `minecraft:debug_sample` | — |
| 31 | `minecraft:delete_chat` | — |
| 32 | `minecraft:disconnect` | `EncodePlayDisconnect` |
| 33 | `minecraft:disguised_chat` | — |
| 34 | `minecraft:entity_event` | `EncodeEntityEvent` |
| 35 | `minecraft:entity_position_sync` | `EncodeEntityPositionSync` |
| 36 | `minecraft:explode` | `EncodeExplosion` |
| 37 | `minecraft:forget_level_chunk` | `EncodeForgetLevelChunk` |
| 38 | `minecraft:game_event` | `EncodeGameEvent` |
| 39 | `minecraft:game_test_highlight_pos` | — |
| 40 | `minecraft:mount_screen_open` | — |
| 41 | `minecraft:hurt_animation` | `EncodeHurtAnimation` |
| 42 | `minecraft:initialize_border` | `EncodeInitializeWorldBorder` |
| 43 | `minecraft:keep_alive` | `EncodeKeepAlivePlay` |
| 44 | `minecraft:level_chunk_with_light` | `world.EncodeChunkDataPacket` |
| 45 | `minecraft:level_event` | `EncodeWorldEvent` |
| 46 | `minecraft:level_particles` | — |
| 47 | `minecraft:light_update` | `world.EncodeLightUpdatePacket` |
| 48 | `minecraft:login` | `EncodeLoginPlay` |
| 49 | `minecraft:map_item_data` | — |
| 50 | `minecraft:merchant_offers` | — |
| 51 | `minecraft:move_entity_pos` | — |
| 52 | `minecraft:move_entity_pos_rot` | — |
| 53 | `minecraft:move_minecart_along_track` | — |
| 54 | `minecraft:move_entity_rot` | — |
| 55 | `minecraft:move_vehicle` | — |
| 56 | `minecraft:open_book` | — |
| 57 | `minecraft:open_screen` | `EncodeOpenScreen` |
| 58 | `minecraft:open_sign_editor` | — |
| 59 | `minecraft:ping` | — |
| 60 | `minecraft:pong_response` | — |
| 61 | `minecraft:place_ghost_recipe` | `EncodePlaceGhostRecipe` |
| 62 | `minecraft:player_abilities` | — |
| 63 | `minecraft:player_chat` | `EncodePlayerChatMessage` |
| 64 | `minecraft:player_combat_end` | — |
| 65 | `minecraft:player_combat_enter` | — |
| 66 | `minecraft:player_combat_kill` | `EncodePlayerCombatKill` |
| 67 | `minecraft:player_info_remove` | `EncodePlayerInfoRemove` |
| 68 | `minecraft:player_info_update` | `EncodePlayerInfoAddPlayer` |
| 69 | `minecraft:player_look_at` | — |
| 70 | `minecraft:player_position` | — |
| 71 | `minecraft:player_rotation` | — |
| 72 | `minecraft:recipe_book_add` | `EncodeRecipeBookAdd` |
| 73 | `minecraft:recipe_book_remove` | `EncodeRecipeBookRemove` |
| 74 | `minecraft:recipe_book_settings` | `EncodeRecipeBookSettings` |
| 75 | `minecraft:remove_entities` | `EncodeRemoveEntities` |
| 76 | `minecraft:remove_mob_effect` | `EncodeRemoveMobEffect` |
| 77 | `minecraft:reset_score` | — |
| 78 | `minecraft:resource_pack_pop` | — |
| 79 | `minecraft:resource_pack_push` | — |
| 80 | `minecraft:respawn` | `EncodeRespawn` |
| 81 | `minecraft:rotate_head` | `EncodeEntityHeadRotation` |
| 82 | `minecraft:section_blocks_update` | — |
| 83 | `minecraft:select_advancements_tab` | — |
| 84 | `minecraft:server_data` | — |
| 85 | `minecraft:set_action_bar_text` | — |
| 86 | `minecraft:set_border_center` | — |
| 87 | `minecraft:set_border_lerp_size` | — |
| 88 | `minecraft:set_border_size` | — |
| 89 | `minecraft:set_border_warning_delay` | — |
| 90 | `minecraft:set_border_warning_distance` | — |
| 91 | `minecraft:set_camera` | — |
| 92 | `minecraft:set_chunk_cache_center` | `EncodeSetCenterChunk` |
| 93 | `minecraft:set_chunk_cache_radius` | — |
| 94 | `minecraft:set_cursor_item` | `EncodeSetCursorItem` |
| 95 | `minecraft:set_default_spawn_position` | `EncodeSetDefaultSpawnPosition` |
| 96 | `minecraft:set_display_objective` | — |
| 97 | `minecraft:set_entity_data` | `EncodeEntityMetadataByte / VarInt / Item / SkinParts` |
| 98 | `minecraft:set_entity_link` | — |
| 99 | `minecraft:set_entity_motion` | `EncodeEntityVelocity` |
| 100 | `minecraft:set_equipment` | `EncodeSetEquipment` |
| 101 | `minecraft:set_experience` | `EncodeSetExperience` |
| 102 | `minecraft:set_health` | `EncodeUpdateHealth` |
| 103 | `minecraft:set_held_slot` | `EncodeSetHeldSlot` |
| 104 | `minecraft:set_objective` | — |
| 105 | `minecraft:set_passengers` | — |
| 106 | `minecraft:set_player_inventory` | `EncodeSetPlayerInventory` |
| 107 | `minecraft:set_player_team` | — |
| 108 | `minecraft:set_score` | — |
| 109 | `minecraft:set_simulation_distance` | — |
| 110 | `minecraft:set_subtitle_text` | — |
| 111 | `minecraft:set_time` | `EncodeUpdateTime` |
| 112 | `minecraft:set_title_text` | — |
| 113 | `minecraft:set_titles_animation` | — |
| 114 | `minecraft:sound_entity` | `EncodeEntitySoundEffect` |
| 115 | `minecraft:sound` | `EncodeSoundEffect` |
| 116 | `minecraft:start_configuration` | — |
| 117 | `minecraft:stop_sound` | — |
| 118 | `minecraft:store_cookie` | — |
| 119 | `minecraft:system_chat` | `EncodeSystemChat` |
| 120 | `minecraft:tab_list` | — |
| 121 | `minecraft:tag_query` | — |
| 122 | `minecraft:take_item_entity` | `EncodeCollect` |
| 123 | `minecraft:teleport_entity` | — |
| 124 | `minecraft:test_instance_block_status` | — |
| 125 | `minecraft:ticking_state` | — |
| 126 | `minecraft:ticking_step` | — |
| 127 | `minecraft:transfer` | — |
| 128 | `minecraft:update_advancements` | — |
| 129 | `minecraft:update_attributes` | — |
| 130 | `minecraft:update_mob_effect` | `EncodeUpdateMobEffect` |
| 131 | `minecraft:update_recipes` | `EncodeUpdateRecipes` |
| 132 | `minecraft:update_tags` | `EncodeUpdateTags` |
| 133 | `minecraft:projectile_power` | — |
| 134 | `minecraft:custom_report_details` | — |
| 135 | `minecraft:server_links` | — |
| 136 | `minecraft:waypoint` | — |
| 137 | `minecraft:clear_dialog` | — |
| 138 | `minecraft:show_dialog` | — |

## Oracle 覆盖情况

`internal/protocol/oracle_test.go` 当前用官方客户端 `StreamCodec` 校验：

- `add_entity`
- `set_entity_data`（TNT 的 fuse 与 block_state 两个字段）
- `explode`
- `block_update`
- `sound`
- `set_entity_motion`
- `remove_entities`
- `entity_event`
- `set_health`
- `player_combat_kill`
- `change_difficulty`
- `update_mob_effect`
- `remove_mob_effect`
- Configuration：`custom_payload`（brand）与 `finish_configuration`

其余包仍列入本清单，后续按同一 oracle 逐批接入。

## 运行方式

准备 1.21.11 客户端 jar 与官方依赖 jar（开发机通常已有
`~/.cache/gmcs/libraries`）。在仓库根目录执行：

```bash
export GMCS_ORACLE=1
export GMCS_CLIENT_JAR="$HOME/.cache/gmcs/versions/1.21.11/client.jar"
# 若依赖不在默认目录：
# export GMCS_MINECRAFT_LIBS="$HOME/.cache/gmcs/libraries"
go test ./internal/protocol -run TestClientboundOracleExplosionPackets -v
```

测试首次运行会调用 `javac` 编译 `tools/packetoracle/ClientOracle.java`，然后启动
一个 Java 进程批量解码所有用例。缺少客户端 jar、Java 或依赖时测试会 Skip，
不会失败。CI 可以把上述环境变量放进受保护的任务，而不是默认测试任务。

Oracle 的约定：

- 输入字节包含包 ID（VarInt）与完整负载；
- 使用官方 `RegistryFriendlyByteBuf` 和 `GameProtocols` /
  `ConfigurationProtocols` 的真实 codec；
- 只有 `decode` 成功且缓冲区没有剩余字节才算通过；
- 失败输出官方异常文本与剩余字节数，便于定位字段错误。

## 版本升级注意

`ClientOracle.java` 通过 1.21.11 的混淆类名反射访问
`SharedConstants`、`Bootstrap`、`BuiltInRegistries`、`RegistryFriendlyByteBuf`、
`GameProtocols` 与 `ConfigurationProtocols`。升级 Minecraft 版本时必须同步
更新这些类名/字段名，否则 oracle 会因反射失败而报错；这不是协议实现代码，
但属于版本升级工具链的一部分。
