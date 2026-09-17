package io.opensourceways.obssdk.internal;

import java.net.InetAddress;

/**
 * 通用字段环境变量解析辅助（对齐 Go 侧 {@code go/internal/env}）。
 *
 * <p>四语言 SDK 统一读同一套 {@code OBS_*} 环境变量（见 spec/common-fields.md），
 * 保证部署配置模板（helm values）不因语言而异。
 *
 * <p>解析优先级：**显式参数 &gt; 环境变量 &gt; 内置默认**。内置默认取契约规定值
 * （spec/common-fields.md「静态字段来源与默认值」）：{@code service} / {@code env} /
 * {@code community} 为 {@code "unknown"}，{@code instance} 为 hostname。
 *
 * <p>为什么要兜底：日志侧的空值一律「省略该键」（见 log/ ObsJsonProvider），
 * 缺了内置默认这几个字段就会**整条从 JSON 里消失**，而不是显示 unknown —— 采集侧
 * 按字段建索引/过滤时会静默漏数；指标侧同理，未设置的字段不会进 label 集，
 * 与 Go / Python / Node 输出的 label 集对不上。
 *
 * <p>本包为 SDK 内部实现，不属于对外 API。
 */
public final class Env {

    /** 字段名/环境变量名：服务名。 */
    public static final String ENV_SERVICE = "OBS_SERVICE";
    /** 字段名/环境变量名：部署环境。 */
    public static final String ENV_ENV = "OBS_ENV";
    /** 字段名/环境变量名：实例标识。 */
    public static final String ENV_INSTANCE = "OBS_INSTANCE";
    /** 字段名/环境变量名：部署级默认社区。 */
    public static final String ENV_COMMUNITY = "OBS_COMMUNITY";

    /** 契约规定的内置默认值（{@code instance} 例外，见 {@link #instance}）。 */
    public static final String DEFAULT_VALUE = "unknown";

    private Env() {
    }

    /** 服务名：显式非空优先，否则读 {@code OBS_SERVICE}，再否则 "unknown"。 */
    public static String service(String explicit) {
        return resolve(explicit, ENV_SERVICE, DEFAULT_VALUE);
    }

    /** 部署环境：显式非空优先，否则读 {@code OBS_ENV}，再否则 "unknown"。 */
    public static String env(String explicit) {
        return resolve(explicit, ENV_ENV, DEFAULT_VALUE);
    }

    /** 实例标识：显式非空优先，否则读 {@code OBS_INSTANCE}，再否则 hostname。 */
    public static String instance(String explicit) {
        return resolve(explicit, ENV_INSTANCE, hostname());
    }

    /** 社区：显式非空优先，否则读 {@code OBS_COMMUNITY}，再否则 "unknown"。 */
    public static String community(String explicit) {
        return resolve(explicit, ENV_COMMUNITY, DEFAULT_VALUE);
    }

    /**
     * 三级解析：显式参数 &gt; 环境变量 &gt; 兜底值。
     * 空串与 null 一样视为「未设置」（与 Go 侧 {@code explicit != ""} 判定一致）。
     */
    static String resolve(String explicit, String envKey, String fallback) {
        if (explicit != null && !explicit.isEmpty()) {
            return explicit;
        }
        String fromEnv = System.getenv(envKey);
        if (fromEnv != null && !fromEnv.isEmpty()) {
            return fromEnv;
        }
        return fallback;
    }

    /**
     * 取本机 hostname，语义对齐 Go 侧 {@code os.Hostname()}（k8s 下即 pod 名）。
     *
     * <p>先读 {@code HOSTNAME} 环境变量：Linux / k8s 由容器运行时写入，无需 DNS 解析、
     * 代价最低；再退回 {@link InetAddress#getLocalHost()}（容器内 /etc/hosts 缺失时
     * 可能抛 {@code UnknownHostException}，故最后兜到 "unknown"）。
     */
    private static String hostname() {
        String fromEnv = System.getenv("HOSTNAME");
        if (fromEnv != null && !fromEnv.isEmpty()) {
            return fromEnv;
        }
        try {
            String fromLookup = InetAddress.getLocalHost().getHostName();
            if (fromLookup != null && !fromLookup.isEmpty()) {
                return fromLookup;
            }
        } catch (Exception ignored) {
            // 解析失败（无 DNS / 无 hosts 记录）→ 退到内置默认
        }
        return DEFAULT_VALUE;
    }
}
