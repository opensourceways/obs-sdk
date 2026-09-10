package io.opensourceways.obssdk.log;

import io.opensourceways.obssdk.ObsSdkConfig;
import io.opensourceways.obssdk.context.RequestContext;
import org.slf4j.MDC;

import java.util.Optional;

/**
 * 日志结构化装配：把部署级默认字段 + 请求级覆盖字段写入 SLF4J MDC，
 * 由接入服务的 logback JSON encoder（logstash-logback-encoder，见
 * {@code examples/logback-json.xml}）输出为单行 JSON。
 *
 * <p>固定键：{@code service / env / instance / community / request_id / trace_id / span_id}
 * （与 spec/common-fields.md、spec/log-format.md 对齐）；{@code trace_id} / {@code span_id}
 * 为二期 trace 预留位，首期恒空、不写入 MDC。
 *
 * <p>community 双层注入与其它语言 SDK 一致：部署级默认来自 {@code OBS_*}/Config，
 * 请求级由中间件在可信判定点解析后经 {@link RequestContext#push} 写入，
 * 本助手在取数时用请求级覆盖默认（同 metrics 的 resolve 语义）。
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
        MDC.put(MDC_SERVICE, nvl(config.service()));
        MDC.put(MDC_ENV, nvl(config.envName()));
        MDC.put(MDC_INSTANCE, nvl(config.instance()));
        MDC.put(MDC_COMMUNITY, nvl(config.community()));
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

        String base = cfg != null ? cfg.community() : null;
        MDC.put(MDC_COMMUNITY, community.orElse(base != null ? base : ""));
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

    /** 请求结束时清理请求级字段，避免线程复用串染（MDC 由框架在线程回收时兜底）。 */
    public static void clearRequestScope() {
        MDC.remove(MDC_REQUEST_ID);
        MDC.remove(MDC_TRACE_ID);
        MDC.remove(MDC_SPAN_ID);
    }

    private static String nvl(String v) {
        return v == null ? "" : v;
    }
}
