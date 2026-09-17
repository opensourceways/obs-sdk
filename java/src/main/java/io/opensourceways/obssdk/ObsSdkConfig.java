package io.opensourceways.obssdk;

import io.opensourceways.obssdk.internal.Env;

/**
 * SDK 静态配置（部署级默认字段）。
 *
 * <p>与 spec/common-fields.md 对齐：统一从环境变量读取默认值
 * {@code OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY}，
 * 语义与 Go/Python/Node SDK 的 Config 一致。
 *
 * <p><b>三级解析</b>（同 Go 侧 {@code internal/env}）：显式参数 &gt; {@code OBS_*}
 * 环境变量 &gt; 内置默认。内置默认为契约规定值：{@code service} / {@code env} /
 * {@code community} = {@code "unknown"}，{@code instance} = hostname（k8s 下即 pod 名）。
 * 因此<b>四个字段取值恒非 null、恒非空</b>——不设兜底时它们会整条从日志 JSON 里消失
 * （provider 对空值省略该键），也进不了指标 label 集。
 */
public final class ObsSdkConfig {

    public static final String ENV_SERVICE = Env.ENV_SERVICE;
    public static final String ENV_ENV = Env.ENV_ENV;
    public static final String ENV_INSTANCE = Env.ENV_INSTANCE;
    public static final String ENV_COMMUNITY = Env.ENV_COMMUNITY;

    private final String service;
    private final String envName;
    private final String instance;
    private final String community;
    private final String namespace;

    private ObsSdkConfig(Builder b) {
        // 构造时即完成三级解析：调用方显式设置 > OBS_* 环境变量 > 内置默认。
        this.service = Env.service(b.service);
        this.envName = Env.env(b.envName);
        this.instance = Env.instance(b.instance);
        this.community = Env.community(b.community);
        this.namespace = b.namespace;
    }

    /** 构建配置；四个部署级字段的取值在 {@link Builder#build()} 时完成解析。 */
    public static Builder builder() {
        return new Builder();
    }

    /**
     * 全部取环境变量/内置默认（测试外入口，等价于 {@code builder().build()}）。
     * 未设置 {@code OBS_*} 的字段回退到契约默认，不会为空。
     */
    public static ObsSdkConfig fromEnvironment() {
        return builder().build();
    }

    /** 服务名（恒非空，未配置时为 "unknown"）。 */
    public String service() {
        return service;
    }

    /** 部署环境（恒非空，未配置时为 "unknown"）。 */
    public String envName() {
        return envName;
    }

    /** 实例标识（恒非空，未配置时取 hostname）。 */
    public String instance() {
        return instance;
    }

    /** 部署级默认社区（恒非空，未配置时为 "unknown"）。 */
    public String community() {
        return community;
    }

    /**
     * 可选命名空间前缀，拼在指标名之前（如 {@code "review"} → {@code review_built_releases}）。
     *
     * <p>默认不设 —— 业务指标按 spec 应以 {@code <service>_} 开头，接入服务可用它把
     * service 前缀一并交给 SDK 拼；**不要**用来补 SDK 保留前缀，spec 不为中间件公共指标
     * 定义任何前缀（同一条 series 已带 {@code service} label）。</p>
     */
    public String namespace() {
        return namespace;
    }

    public Builder toBuilder() {
        return new Builder()
                .service(service)
                .env(envName)
                .instance(instance)
                .community(community)
                .namespace(namespace);
    }

    public static final class Builder {
        private String service;
        private String envName;
        private String instance;
        private String community;
        private String namespace;

        public Builder service(String v) {
            this.service = v;
            return this;
        }

        public Builder env(String v) {
            this.envName = v;
            return this;
        }

        public Builder instance(String v) {
            this.instance = v;
            return this;
        }

        public Builder community(String v) {
            this.community = v;
            return this;
        }

        public Builder namespace(String v) {
            this.namespace = v;
            return this;
        }

        public ObsSdkConfig build() {
            return new ObsSdkConfig(this);
        }
    }
}
