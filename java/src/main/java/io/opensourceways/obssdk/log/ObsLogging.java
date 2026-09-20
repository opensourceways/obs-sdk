package io.opensourceways.obssdk.log;

import io.opensourceways.obssdk.ObsSdkConfig;
import io.opensourceways.obssdk.context.RequestContext;
import io.opensourceways.obssdk.internal.Env;
import org.slf4j.MDC;

import java.util.Optional;

/**
 * 日志结构化装配：持有全局部署级配置，并把请求级覆盖字段写入 SLF4J MDC，
 * 由接入服务的 logback JSON encoder（logstash-logback-encoder，见
 * {@code examples/logback-json.xml}）输出为单行 JSON。
 *
 * <p>固定键：{@code service / env / instance / community / request_id / trace_id / span_id}
 * （与 spec/common-fields.md、spec/log-format.md 对齐）；{@code trace_id} / {@code span_id}
 * 为二期 trace 预留位，首期恒空、不写入 MDC。
 *
 * <p>部署级字段（{@code service / env / instance / community} 默认值）本质是全局静态配置
 * （来源 {@code OBS_*} / Config），<b>不随请求线程变化</b>，因此不走线程本地 MDC ——
 * {@link ObsJsonProvider} 直接从本类的全局静态访问器取数。MDC 只承载请求级字段：
 * {@code community} 请求覆盖（可信判定点解析后经 {@link RequestContext#push} 写入）
 * 优先于部署默认，{@code request_id} 由中间件注入。
 */
public final class ObsLogging {

    public static final String MDC_SERVICE = "service";
    public static final String MDC_ENV = "env";
    public static final String MDC_INSTANCE = "instance";
    public static final String MDC_COMMUNITY = "community";
    public static final String MDC_REQUEST_ID = "request_id";
    public static final String MDC_TRACE_ID = "trace_id";
    public static final String MDC_SPAN_ID = "span_id";

    private static volatile ObsSdkConfig cfg;

    private ObsLogging() {
    }

    /** 全局初始化一次：登记部署级默认字段（通常服务启动时调用）。 */
    public static void init(ObsSdkConfig config) {
        cfg = config;
    }

    /**
     * 请求进入时刷新请求级字段：request_id 取显式值（无则保持已有的），
     * community 取请求覆盖，未覆盖回退部署默认；随后交由 JSON encoder 输出。
     * 返回前先写入 MDC；{@link RequestContext} 的 scope 由中间件管理。
     */
    public static void enrich(RequestContext request) {
        Optional<String> community = request != null && request.community() != null
                ? Optional.of(request.community())
                : Optional.empty();
        Optional<String> requestId = request != null && request.requestId() != null
                ? Optional.of(request.requestId())
                : Optional.empty();
        Optional<String> traceId = request != null && request.traceId() != null
                ? Optional.of(request.traceId())
                : Optional.empty();
        Optional<String> spanId = request != null && request.spanId() != null
                ? Optional.of(request.spanId())
                : Optional.empty();

        MDC.put(MDC_COMMUNITY, community.orElse(deploymentCommunity()));
        if (requestId.isPresent()) {
            MDC.put(MDC_REQUEST_ID, requestId.get());
        }
        if (traceId.isPresent()) {
            MDC.put(MDC_TRACE_ID, traceId.get());
        }
        if (spanId.isPresent()) {
            MDC.put(MDC_SPAN_ID, spanId.get());
        }
    }

    /**
     * 请求结束时清理请求级字段，避免线程复用串染（MDC 由框架在线程回收时兜底）。
     *
     * <p>{@code community} 必须**复位为部署级默认**而不是删掉或留着：MDC 是线程本地的，
     * 而 web 容器的请求线程是复用的 —— 删掉会连部署默认一起丢（字段整个消失），
     * 留着则会把上一个请求的覆盖值（如 mindspore）串给下一个请求之外打出的日志
     * （定时任务 / 异步回调），且不报错，属于静默污染：按 community 的聚合与告警会失真。
     */
    public static void clearRequestScope() {
        MDC.remove(MDC_REQUEST_ID);
        MDC.remove(MDC_TRACE_ID);
        MDC.remove(MDC_SPAN_ID);
        MDC.put(MDC_COMMUNITY, deploymentCommunity());
    }

    /** 部署级 service；{@code init} 未调用时回退契约默认（见 internal/Env）。 */
    public static String deploymentService() {
        ObsSdkConfig current = cfg;
        return current != null && current.service() != null ? current.service() : Env.DEFAULT_VALUE;
    }

    /** 部署级 env；{@code init} 未调用时回退契约默认（见 internal/Env）。 */
    public static String deploymentEnv() {
        ObsSdkConfig current = cfg;
        return current != null && current.envName() != null ? current.envName() : Env.DEFAULT_VALUE;
    }

    /** 部署级 instance；{@code init} 未调用时回退契约默认（见 internal/Env）。 */
    public static String deploymentInstance() {
        ObsSdkConfig current = cfg;
        return current != null && current.instance() != null ? current.instance() : Env.DEFAULT_VALUE;
    }

    /** 部署级默认 community；{@code init} 未调用时回退契约默认（见 internal/Env）。 */
    public static String deploymentCommunity() {
        ObsSdkConfig current = cfg;
        return current != null && current.community() != null ? current.community() : Env.DEFAULT_VALUE;
    }
}
