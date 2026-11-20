//[https://github.com/xhdndmm/gmcs]
//MIT License
//Copyright (c) 2026 喜欢电脑的猫咪

// packetoracle：用 1.21.11 客户端 jar 的真实 clientbound StreamCodec 解码
// gmcs 编码的包，校验字段布局、序列化器与 Holder 编码。
//
// 该文件按 1.21.11 的混淆名反射访问官方类；升级 Minecraft 版本时必须同步
// 更新这里的类名/字段名（见 docs/CONTRIBUTING.md 第 3 节）。不要在 oracle 里
// 手写字段解析，否则会失去“官方解码器真值”的意义。
//
// 输入（stdin，每行一个用例）：<play|configuration> <十六进制字节>
// 十六进制字节必须包含包 ID（VarInt），与网络上的包负载一致。
// 输出（stdout，每行一个 JSON）：{"ok":bool,"class":"...","remaining":n}
// 或 {"ok":false,"error":"...","remaining":n}。
//
// 运行需要客户端 jar 与官方依赖 jar；仓库内的 Go 测试会负责拼 classpath。
package oracle;

import io.netty.buffer.ByteBuf;
import io.netty.buffer.Unpooled;
import java.io.BufferedReader;
import java.io.FileDescriptor;
import java.io.FileOutputStream;
import java.io.InputStreamReader;
import java.io.PrintStream;
import java.lang.reflect.*;
import java.util.function.Function;

public final class ClientOracle {
    static Object registryAccess;
    static final PrintStream OUT = new PrintStream(new FileOutputStream(FileDescriptor.out), true);

    public static void main(String[] args) throws Exception {
        // SharedConstants.tryDetectVersion()，随后 Bootstrap.bootStrap()。
        // 不初始化这些，注册表与游戏版本相关的 codec 会抛 "Game version not set"。
        Class.forName("w").getDeclaredMethod("a").invoke(null);
        Class.forName("amv").getDeclaredMethod("a").invoke(null);

        Class<?> builtin = Class.forName("mi");
        Field writable = builtin.getDeclaredField("aU");
        writable.setAccessible(true);
        Class<?> registryAccessClass = Class.forName("jr");
        registryAccess = registryAccessClass
                .getDeclaredMethod("a", Class.forName("jq"))
                .invoke(null, writable.get(null));

        Object playCodec = playCodec();
        Object configurationCodec = configurationCodec();

        BufferedReader reader = new BufferedReader(new InputStreamReader(System.in));
        String line;
        while ((line = reader.readLine()) != null) {
            line = line.trim();
            if (line.isEmpty() || line.startsWith("#")) {
                continue;
            }
            int space = line.indexOf(' ');
            if (space <= 0) {
                OUT.println(errorJSON("invalid input line", 0));
                continue;
            }
            String stage = line.substring(0, space);
            byte[] payload;
            try {
                payload = hex(line.substring(space + 1));
            } catch (RuntimeException e) {
                OUT.println(errorJSON("invalid hex: " + e.getMessage(), 0));
                continue;
            }
            Object codec;
            if (stage.equals("play")) {
                codec = playCodec;
            } else if (stage.equals("configuration")) {
                codec = configurationCodec;
            } else {
                OUT.println(errorJSON("unknown stage: " + stage, payload.length));
                continue;
            }
            OUT.println(decode(codec, payload));
        }
    }

    static Object playCodec() throws Exception {
        // GameProtocols.c 是 clientbound play 的 ProtocolInfoBuilder。
        Object base = Class.forName("aia").getField("c").get(null);
        Object decorator = Class.forName("xq")
                .getMethod("a", Class.forName("jr"))
                .invoke(null, registryAccess);
        return Class.forName("abe")
                .getMethod("a", Function.class)
                .invoke(base, decorator);
    }

    static Object configurationCodec() throws Exception {
        // ConfigurationProtocols.d 是已构建的 clientbound configuration 协议。
        return Class.forName("acp").getField("d").get(null);
    }

    static String decode(Object protocolInfo, byte[] payload) {
        ByteBuf raw = Unpooled.wrappedBuffer(payload);
        try {
            Object registryBuffer = Class.forName("xq")
                    .getConstructor(ByteBuf.class, Class.forName("jr"))
                    .newInstance(raw, registryAccess);
            Object streamCodec = Class.forName("xn")
                    .getMethod("c")
                    .invoke(protocolInfo);
            Method decode = streamCodec.getClass().getMethod("decode", Object.class);
            decode.setAccessible(true);
            Object packet = decode.invoke(streamCodec, registryBuffer);
            int remaining = raw.readableBytes();
            String className = packet.getClass().getName();
            if (remaining != 0) {
                return "{\"ok\":false,\"class\":\"" + escape(className)
                        + "\",\"error\":\"decoder left trailing bytes\",\"remaining\":"
                        + remaining + "}";
            }
            return "{\"ok\":true,\"class\":\"" + escape(className) + "\",\"remaining\":0}";
        } catch (InvocationTargetException e) {
            Throwable cause = e.getCause();
            return errorJSON(cause == null ? e.toString() : cause.toString(), raw.readableBytes());
        } catch (Throwable t) {
            return errorJSON(t.toString(), raw.readableBytes());
        }
    }

    static String errorJSON(String message, int remaining) {
        return "{\"ok\":false,\"error\":\"" + escape(message) + "\",\"remaining\":"
                + remaining + "}";
    }

    static String escape(String value) {
        return value.replace("\\", "\\\\").replace("\"", "\\\"")
                .replace("\n", "\\n").replace("\r", "\\r");
    }

    static byte[] hex(String value) {
        String text = value.replaceAll("\\s", "");
        if ((text.length() & 1) != 0) {
            throw new IllegalArgumentException("odd number of hex digits");
        }
        byte[] result = new byte[text.length() / 2];
        for (int i = 0; i < result.length; i++) {
            result[i] = (byte) Integer.parseInt(text.substring(i * 2, i * 2 + 2), 16);
        }
        return result;
    }
}
