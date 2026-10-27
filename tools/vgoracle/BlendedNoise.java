// blendednoise：从官方 jar 导出 BlendedNoise（old_blended_noise）的真值。
//
// 参数：<lo> <hi> 为 Xoroshiro 128 位状态的十六进制（无符号），
// 由 Go 侧 positionalRandomFactory.FromHashOf("minecraft:terrain") 派生。
//
// 用法：
//   javac -cp "" -d out BlendedNoise.java
//   java -cp "out:<server.jar>:<libraries>/*" oracle.BlendedNoise <lo> <hi>
package oracle;

import java.lang.reflect.*;

public final class BlendedNoise {
    public static void main(String[] args) throws Exception {
        // 许多密度函数类在静态初始化时会访问注册表，因此必须先 bootstrap。
        Class<?> bootstrap = Class.forName("amv"); // net.minecraft.server.Bootstrap
        Method bootStrap = bootstrap.getDeclaredMethod("a"); // bootStrap()
        bootStrap.setAccessible(true);
        bootStrap.invoke(null);

        long lo = Long.parseUnsignedLong(args[0], 16);
        long hi = Long.parseUnsignedLong(args[1], 16);

        Class<?> randomSource = Class.forName("bgr"); // RandomSource
        Class<?> xoroshiro = Class.forName("evr");    // XoroshiroRandomSource
        Constructor<?> stateCtor = xoroshiro.getDeclaredConstructor(long.class, long.class);
        Object random = stateCtor.newInstance(lo, hi);

        Class<?> blended = Class.forName("fkb");      // BlendedNoise
        Constructor<?> ctor = blended.getDeclaredConstructor(
                randomSource, double.class, double.class, double.class, double.class, double.class);
        Object noise = ctor.newInstance(random, 0.25, 0.125, 80.0, 160.0, 8.0);

        Class<?> ctxClass = Class.forName("euj$e");   // SinglePointContext
        Constructor<?> ctxCtor = ctxClass.getDeclaredConstructor(int.class, int.class, int.class);
        Method compute = blended.getDeclaredMethod("a", Class.forName("euj$b"));

        int[][] points = {
            {0, 0, 0}, {16, 64, 32}, {-123, -64, 456}, {1600, 320, -1600},
            {15, 44, 12}, {15, 32, 12}, {8, 48, 8}, {0, 64, 0},
            {-1000, 70, 2000}, {123456, -20, -654321},
        };
        for (int[] p : points) {
            Object ctx = ctxCtor.newInstance(p[0], p[1], p[2]);
            double value = (Double) compute.invoke(noise, ctx);
            System.out.printf("%d %d %d %.17g%n", p[0], p[1], p[2], value);
        }
    }
}
