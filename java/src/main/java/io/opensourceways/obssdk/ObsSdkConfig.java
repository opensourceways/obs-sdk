package io.opensourceways.obssdk;

/**
 * SDK 静态配置（部署级默认字段）。
 *
 * <p>与 spec/common-fields.md 对齐：统一从环境变量读取默认值
 * {@code OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY}，
 * 语义与 Go/Python/Node SDK 的 Config 一致。
 */
public final class ObsSdkConfig {

    public static final String ENV_SERVICE = "OBS_SERVICE";
    public static final String ENV_ENV = "OBS_ENV";
    public static final String ENV_INSTANCE = "OBS_INSTANCE";
    public static final String ENV_COMMUNITY = "OBS_COMMUNITY";

    private final String service;
    private final String envName;
    private final String instance;
    private final String community;
    private final String namespace;

    private ObsSdkConfig(Builder b) {
        this.service = b.service;
        this.envName = b.envName;
        this.instance = b.instance;
        this.community = b.community;
        this.namespace = b.namespace;
    }

    /** 按规范优先级读取环境变量；调用方显式设置的值优先。 */
    public static Builder builder() {
        return new Builder();
    }

    /** 只从环境变量构造（测试外入口）。 */
    public static ObsSdkConfig fromEnvironment() {
        return builder()
                .service(System.getenv(ENV_SERVICE))
                .env(System.getenv(ENV_ENV))
                .instance(System.getenv(ENV_INSTANCE))
                .community(System.getenv(ENV_COMMUNITY))
                .build();
    }

    public String service() {
        return service;
    }

    public String envName() {
        return envName;
    }

    public String instance() {
        return instance;
    }

    public String community() {
        return community;
    }

    /** 可选命名空间前缀（跨服务共享 SDK 埋点时用 obs_ 等前缀区分，见 spec/metrics-format.md）。 */
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
