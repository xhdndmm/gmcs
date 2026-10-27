// biomeparams：从官方 Minecraft jar 导出 overworld 多噪声群系源的参数表。
//
// 原版 overworld 的参数表（MultiNoiseBiomeSourceParameterList.Preset.OVERWORLD）
// 是**代码内建**的，不在数据包里（data/minecraft/worldgen/
// multi_noise_biome_source_parameter_list/overworld.json 只有一个 preset 引用），
// 因此只能用反射从官方类里导出。
//
// 导出的坐标是原版量化后的 long（Mth.quantizeCoord：去掉 1e-4 精度），这样 Go 侧
// 可以直接做整数比较，避免浮点往返误差。
//
// 运行方式：
//   javac -cp "" -d out BiomeParameters.java
//   java -cp "out:<server.jar>:<libraries>/*" oracle.BiomeParameters <out.json>
package oracle;

import java.lang.reflect.*;
import java.util.*;

public final class BiomeParameters {
    public static void main(String[] args) throws Exception {
        Class<?> presetClass = Class.forName("dyc$a"); // MultiNoiseBiomeSourceParameterList$Preset
        Object overworld = presetClass.getDeclaredField("b").get(null); // Preset.OVERWORLD
        if (overworld == null) {
            throw new IllegalStateException("Preset.OVERWORLD 为空");
        }

        // private static <T> Climate.ParameterList<T> generateOverworldBiomes(Function<ResourceKey<Biome>, T>)
        Method generate = null;
        for (Method m : presetClass.getDeclaredMethods()) {
            if (m.getParameterCount() == 1 && m.getParameterTypes()[0] == java.util.function.Function.class
                    && Modifier.isStatic(m.getModifiers()) && m.getReturnType().getName().equals("dxx$c")) {
                generate = m;
                break;
            }
        }
        if (generate == null) {
            throw new IllegalStateException("未找到 generateOverworldBiomes");
        }
        generate.setAccessible(true);
        java.util.function.Function<Object, Object> identity = key -> key;
        Object parameterList = generate.invoke(null, identity);

        Field valuesField = parameterList.getClass().getDeclaredField("a"); // List<Pair<ParameterPoint, T>>
        valuesField.setAccessible(true);
        @SuppressWarnings("unchecked")
        List<Object> values = (List<Object>) valuesField.get(parameterList);

        Class<?> pointClass = Class.forName("dxx$d");  // Climate.ParameterPoint
        Class<?> spanClass = Class.forName("dxx$b");   // Climate.ParameterSpan
        Method spanMin = spanClass.getMethod("a");
        Method spanMax = spanClass.getMethod("b");
        Method[] spans = new Method[6];
        String[] names = {"temperature", "humidity", "continentalness", "erosion", "depth", "weirdness"};
        String[] accessors = {"b", "c", "d", "e", "f", "g"};
        for (int i = 0; i < 6; i++) {
            spans[i] = pointClass.getMethod(accessors[i]);
        }
        Method offset = pointClass.getMethod("h");

        StringBuilder out = new StringBuilder();
        out.append("{\n  \"source\": \"MultiNoiseBiomeSourceParameterList.Preset.OVERWORLD\",\n");
        out.append("  \"quantize_scale\": 10000,\n  \"points\": [\n");
        for (int idx = 0; idx < values.size(); idx++) {
            Object pair = values.get(idx);
            Method getFirst = pair.getClass().getMethod("getFirst");
            Method getSecond = pair.getClass().getMethod("getSecond");
            Object point = getFirst.invoke(pair);
            Object biomeKey = getSecond.invoke(pair);
            // ResourceKey.identifier() -> a（registry() 是 b，会给出 "minecraft:worldgen/biome"）
            Method identifier = biomeKey.getClass().getMethod("a");
            Object id = identifier.invoke(biomeKey);

            out.append("    {\"biome\": \"").append(id).append("\"");
            for (int i = 0; i < 6; i++) {
                Object span = spans[i].invoke(point);
                long min = (Long) spanMin.invoke(span);
                long max = (Long) spanMax.invoke(span);
                out.append(", \"").append(names[i]).append("\": [").append(min).append(", ").append(max).append("]");
            }
            out.append(", \"offset\": ").append((Long) offset.invoke(point));
            out.append(idx + 1 < values.size() ? "},\n" : "}\n");
        }
        out.append("  ]\n}\n");

        java.nio.file.Files.writeString(java.nio.file.Path.of(args[0]), out.toString());
        System.out.println("wrote " + values.size() + " points to " + args[0]);
    }
}
