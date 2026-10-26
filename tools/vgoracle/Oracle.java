// vgoracle：从官方 Minecraft jar 导出噪声实现的真值，用于逐值对拍 Go 实现。
//
// 官方类是混淆名（见 mappings），因此这里用反射按“参数类型/返回类型”挑选成员，
// 不依赖方法名：
//   bgr   = RandomSource            evr = XoroshiroRandomSource (必须直接构造：
//                                     RandomSource.create(long) 在 1.21.11 返回
//                                     LegacyRandomSource，不是 Xoroshiro)
//   fkc   = ImprovedNoise           fke = NormalNoise, fke$a = NoiseParameters
//
// 运行方式（classpath = 官方 server jar + 官方依赖库）：
//   javac -cp "" -d out Oracle.java
//   java -cp "out:" oracle.Oracle internal/world/vanilla/testdata/noise_oracle.json
// 注意：类必须放在非默认包（官方 jar 是签名 jar，同名默认包会报签名冲突）。
package oracle;

import java.lang.reflect.*;
import java.util.*;

// 通过反射调用官方 jar 中混淆后的噪声实现，输出对照数据（JSON）。
// 用法：java -cp "<server.jar>:<libraries>/*" Oracle <out.json>
public final class Oracle {
    static Class<?> randomSource;
    static Method createLong;
    static Method nextLong;
    static Constructor<?> xoroshiroFactory;

    public static void main(String[] args) throws Exception {
        randomSource = Class.forName("bgr"); // net.minecraft.util.RandomSource
        Class<?> xoroshiro = Class.forName("evr");                   // XoroshiroRandomSource
        Constructor<?> xoroshiroCtor = xoroshiro.getDeclaredConstructor(long.class);
        createLong = null; // 改用直接构造 Xoroshiro，避免 create(long) 走 LegacyRandomSource
        xoroshiroFactory = xoroshiroCtor;
        nextLong = randomSource.getDeclaredMethod("g");               // nextLong()
        nextLong.setAccessible(true);

        StringBuilder out = new StringBuilder();
        out.append("{\n");

        // 1) nextLong 序列
        long[] seeds = {0L, 1L, 42L, 12345L};
        out.append("  \"nextLong\": [\n");
        for (int i = 0; i < seeds.length; i++) {
            Object random = xoroshiroFactory.newInstance(seeds[i]);
            out.append("    {\"seed\": ").append(seeds[i]).append(", \"values\": [");
            for (int k = 0; k < 8; k++) {
                if (k > 0) out.append(", ");
                out.append(((Long) nextLong.invoke(random)).toString());
            }
            out.append("]}");
            out.append(i + 1 < seeds.length ? ",\n" : "\n");
        }
        out.append("  ],\n");

        // 2) ImprovedNoise（Perlin）取值
        Class<?> improved = Class.forName("fkc");                       // ImprovedNoise
        Constructor<?> improvedCtor = improved.getDeclaredConstructor(randomSource);
        improvedCtor.setAccessible(true);
        Method improvedNoise = improved.getDeclaredMethod("a", double.class, double.class, double.class);
        improvedNoise.setAccessible(true);
        double[][] points = {
            {0.0, 0.0, 0.0},
            {0.5, 0.0, 0.0},
            {12.34, 56.78, -90.12},
            {1000.5, 64.0, -2000.25},
            {-0.5, 0.25, 0.75},
        };
        out.append("  \"improvedNoise\": [\n");
        for (int i = 0; i < seeds.length; i++) {
            Object random = xoroshiroFactory.newInstance(seeds[i]);
            Object noise = improvedCtor.newInstance(random);
            out.append("    {\"seed\": ").append(seeds[i]).append(", \"values\": [");
            for (int p = 0; p < points.length; p++) {
                if (p > 0) out.append(", ");
                double value = (Double) improvedNoise.invoke(noise, points[p][0], points[p][1], points[p][2]);
                out.append(dbl(value));
            }
            out.append("]}");
            out.append(i + 1 < seeds.length ? ",\n" : "\n");
        }
        out.append("  ],\n");

        // 3) NormalNoise 取值（用原版 continentalness 与 cave_cheese 参数）
        Class<?> paramsClass = Class.forName("fke$a");                   // NormalNoise.NoiseParameters
        Constructor<?> paramsCtor = paramsClass.getDeclaredConstructor(int.class, double.class, double[].class);
        paramsCtor.setAccessible(true);
        Class<?> normal = Class.forName("fke");                          // NormalNoise
        // 显式按混淆名 a 取 create：b 是 createLegacy，它会用 forkPositional 派生每个八度的
        // 随机源，推进方式与非 legacy 路径完全不同，不能作为非 legacy 的对拍基准。
        Method normalCreate = normal.getDeclaredMethod("a", randomSource, paramsClass);
        normalCreate.setAccessible(true);
        Method normalValue = null;
        for (Method m : normal.getDeclaredMethods()) {
            if (m.getParameterCount() == 3 && m.getParameterTypes()[0] == double.class && m.getParameterTypes()[1] == double.class && m.getParameterTypes()[2] == double.class && m.getReturnType() == double.class) { normalValue = m; break; }
        }
        if (normalValue == null) throw new IllegalStateException("getValue not found");
        normalValue.setAccessible(true);
        Object[][] cases = {
            {"continentalness", -9, new double[]{1, 1, 2, 2, 2, 1, 1, 1, 1}},
            {"cave_cheese", -8, new double[]{0.5, 1, 2, 1, 2, 1, 0, 0, 0}},
        };
        out.append("  \"normalNoise\": [\n");
        for (int c = 0; c < cases.length; c++) {
            String name = (String) cases[c][0];
            int firstOctave = (Integer) cases[c][1];
            double[] amplitudes = (double[]) cases[c][2];
            Object params = paramsCtor.newInstance(firstOctave, amplitudes[0], java.util.Arrays.copyOfRange(amplitudes, 1, amplitudes.length));
            for (int i = 0; i < seeds.length; i++) {
                Object random = xoroshiroFactory.newInstance(seeds[i]);
                Object noise = normalCreate.invoke(null, random, params);
                out.append("    {\"noise\": \"").append(name).append("\", \"seed\": ").append(seeds[i]).append(", \"values\": [");
                for (int p = 0; p < points.length; p++) {
                    if (p > 0) out.append(", ");
                    double value = (Double) normalValue.invoke(noise, points[p][0], points[p][1], points[p][2]);
                    out.append(dbl(value));
                }
                out.append("]}");
                boolean last = (c == cases.length - 1) && (i + 1 == seeds.length);
                out.append(last ? "\n" : ",\n");
            }
        }
        out.append("  ],\n");

        // 3b) NormalNoise 的内部细节：valueFactor/maxValue 与两份 PerlinNoise 各自的取值，
        //     对拍失败时用于判断问题出在随机源推进、八度振幅还是归一化因子。
        Field fValueFactor = normal.getDeclaredField("c");
        Field fFirstNoise = normal.getDeclaredField("d");
        Field fSecondNoise = normal.getDeclaredField("e");
        Field fMaxValue = normal.getDeclaredField("f");
        fValueFactor.setAccessible(true);
        fFirstNoise.setAccessible(true);
        fSecondNoise.setAccessible(true);
        fMaxValue.setAccessible(true);
        Class<?> perlin = Class.forName("fkf");                           // PerlinNoise
        Method perlinValue = perlin.getDeclaredMethod("a", double.class, double.class, double.class);
        perlinValue.setAccessible(true);
        out.append("  \"normalNoiseDetail\": [\n");
        for (int c = 0; c < cases.length; c++) {
            String name = (String) cases[c][0];
            int firstOctave = (Integer) cases[c][1];
            double[] amplitudes = (double[]) cases[c][2];
            Object params = paramsCtor.newInstance(firstOctave, amplitudes[0], java.util.Arrays.copyOfRange(amplitudes, 1, amplitudes.length));
            for (int i = 0; i < seeds.length; i++) {
                Object random = xoroshiroFactory.newInstance(seeds[i]);
                Object noise = normalCreate.invoke(null, random, params);
                Object firstNoise = fFirstNoise.get(noise);
                Object secondNoise = fSecondNoise.get(noise);
                out.append("    {\"noise\": \"").append(name).append("\", \"seed\": ").append(seeds[i]);
                out.append(", \"valueFactor\": ").append(dbl((Double) fValueFactor.get(noise)));
                out.append(", \"maxValue\": ").append(dbl((Double) fMaxValue.get(noise)));
                out.append(", \"first\": [");
                for (int p = 0; p < points.length; p++) {
                    if (p > 0) out.append(", ");
                    out.append(dbl((Double) perlinValue.invoke(firstNoise, points[p][0], points[p][1], points[p][2])));
                }
                out.append("], \"second\": [");
                for (int p = 0; p < points.length; p++) {
                    if (p > 0) out.append(", ");
                    out.append(dbl((Double) perlinValue.invoke(secondNoise, points[p][0], points[p][1], points[p][2])));
                }
                out.append("]}");
                boolean last = (c == cases.length - 1) && (i + 1 == seeds.length);
                out.append(last ? "\n" : ",\n");
            }
        }
        out.append("  ],\n");

        // 3c) 世界生成真正使用的 legacy 噪声。
        //     Noises.createNormalNoise 调用的是 NormalNoise.createLegacy（混淆名 b），
        //     其 PerlinNoise 走 forkPositional + fromHashOf("octave_<n>") 派生每个八度
        //     的随机源，与非 legacy 路径的随机数消耗顺序完全不同。
        Method normalCreateLegacy = normal.getDeclaredMethod("b", randomSource, paramsClass);
        normalCreateLegacy.setAccessible(true);
        out.append("  \"normalNoiseLegacy\": [\n");
        for (int c = 0; c < cases.length; c++) {
            String name = (String) cases[c][0];
            int firstOctave = (Integer) cases[c][1];
            double[] amplitudes = (double[]) cases[c][2];
            Object params = paramsCtor.newInstance(firstOctave, amplitudes[0], java.util.Arrays.copyOfRange(amplitudes, 1, amplitudes.length));
            for (int i = 0; i < seeds.length; i++) {
                Object random = xoroshiroFactory.newInstance(seeds[i]);
                Object noise = normalCreateLegacy.invoke(null, random, params);
                out.append("    {\"noise\": \"").append(name).append("\", \"seed\": ").append(seeds[i]).append(", \"values\": [");
                for (int p = 0; p < points.length; p++) {
                    if (p > 0) out.append(", ");
                    double value = (Double) normalValue.invoke(noise, points[p][0], points[p][1], points[p][2]);
                    out.append(dbl(value));
                }
                out.append("]}");
                boolean last = (c == cases.length - 1) && (i + 1 == seeds.length);
                out.append(last ? "\n" : ",\n");
            }
        }
        out.append("  ],\n");

        // 3d) legacy 噪声里第一份 PerlinNoise 每个八度的 ImprovedNoise 偏移，
        //     用于逐项验证 forkPositional + seedFromHashOf 的派生链。
        Field perlinLevels = perlin.getDeclaredField("b");
        perlinLevels.setAccessible(true);
        Field fxoOf = improved.getDeclaredField("a");
        Field fyoOf = improved.getDeclaredField("b");
        Field fzoOf = improved.getDeclaredField("c");
        fxoOf.setAccessible(true);
        fyoOf.setAccessible(true);
        fzoOf.setAccessible(true);
        out.append("  \"normalNoiseLegacyState\": [\n");
        for (int c = 0; c < cases.length; c++) {
            String name = (String) cases[c][0];
            int firstOctave = (Integer) cases[c][1];
            double[] amplitudes = (double[]) cases[c][2];
            Object params = paramsCtor.newInstance(firstOctave, amplitudes[0], java.util.Arrays.copyOfRange(amplitudes, 1, amplitudes.length));
            Object random = xoroshiroFactory.newInstance(seeds[0]);
            Object noise = normalCreateLegacy.invoke(null, random, params);
            Object[] levels = (Object[]) perlinLevels.get(fFirstNoise.get(noise));
            out.append("    {\"noise\": \"").append(name).append("\", \"seed\": ").append(seeds[0]);
            out.append(", \"octaves\": [");
            boolean firstOctaveEntry = true;
            for (int k = 0; k < levels.length; k++) {
                if (levels[k] == null) continue;
                if (!firstOctaveEntry) out.append(", ");
                firstOctaveEntry = false;
                out.append("{\"octave\": ").append(firstOctave + k);
                out.append(", \"xo\": ").append(dbl((Double) fxoOf.get(levels[k])));
                out.append(", \"yo\": ").append(dbl((Double) fyoOf.get(levels[k])));
                out.append(", \"zo\": ").append(dbl((Double) fzoOf.get(levels[k])));
                out.append("}");
            }
            out.append("]}");
            out.append(c + 1 < cases.length ? ",\n" : "\n");
        }
        out.append("  ],\n");

        // 4) ImprovedNoise 的内部状态（xo/yo/zo 与 256 项置换表），
        //    用于校验 Go 侧的构造与 Fisher–Yates 洗牌是否与原版一致。
        Field fxo = improved.getDeclaredField("a");
        Field fyo = improved.getDeclaredField("b");
        Field fzo = improved.getDeclaredField("c");
        Field fp = improved.getDeclaredField("e");
        fxo.setAccessible(true);
        fyo.setAccessible(true);
        fzo.setAccessible(true);
        fp.setAccessible(true);
        out.append("  \"improvedNoiseState\": [\n");
        for (int i = 0; i < seeds.length; i++) {
            Object random = xoroshiroFactory.newInstance(seeds[i]);
            Object noise = improvedCtor.newInstance(random);
            byte[] table = (byte[]) fp.get(noise);
            out.append("    {\"seed\": ").append(seeds[i]);
            out.append(", \"xo\": ").append(dbl((Double) fxo.get(noise)));
            out.append(", \"yo\": ").append(dbl((Double) fyo.get(noise)));
            out.append(", \"zo\": ").append(dbl((Double) fzo.get(noise)));
            out.append(", \"p\": [");
            for (int k = 0; k < table.length; k++) {
                if (k > 0) out.append(", ");
                out.append(table[k] & 0xff);
            }
            out.append("]}");
            out.append(i + 1 < seeds.length ? ",\n" : "\n");
        }
        out.append("  ]\n}\n");

        java.nio.file.Files.writeString(java.nio.file.Path.of(args[0]), out.toString());
        System.out.println("wrote " + args[0]);
    }

    static String dbl(double value) {
        return Double.toString(value);
    }
}
