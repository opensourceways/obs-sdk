package io.opensourceways.obssdk.context;

import java.util.HashMap;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Supplier;

/**
 * 请求级上下文：承载 {@code community / request_id / trace_id} 三个请求字段，
 * 语义与其它语言 SDK 对齐 —— Go sdkctx(context.Context)、Python contextvars、Node AsyncLocalStorage。
 *
 * <p>community 双层注入（见 spec/common-fields.md、community-values.md）：
 * 第一层是部署级默认（静态，来自 OBS_* 环境变量）；第二层是请求上下文动态覆盖。
 * 中间件在服务自己的可信判定点（路由前缀 / 认证主体 / 白名单）解析出请求级 community
 * 后，通过 {@link #push} 写入本上下文；指标与日志读取 {@link #communityOverride()}，
 * 未覆盖时回退部署默认。
 *
 * <p>{@code trace_id} 为预留位（首期不做 trace）：本次只保证字段在上下文中可写入、可透传，
 * 供后续 trace 接入时读取，不产生任何 span。
 */
public final class RequestContext {

    /** 当前线程请求字段。继承式 ThreadLocal：子线程能读到父线程（HTTP 场景足够）。 */
    private static final InheritableThreadLocal<RequestContext> HOLDER = new InheritableThreadLocal<>();

    private final String community;
    private final String requestId;
    private final String traceId;

    private RequestContext(String community, String requestId, String traceId) {
        this.community = community;
        this.requestId = requestId;
        this.traceId = traceId;
    }

    /** 构造一个请求字段快照（通常由中间件调用）。 */
    public static RequestContext of(String community, String requestId, String traceId) {
        return new RequestContext(community, requestId, traceId);
    }

    /** 用给定字段绑定当前线程，返回作用域句柄；离开作用域（close）后自动还原。 */
    public static Scope push(String community, String requestId, String traceId) {
        return push(of(community, requestId, traceId));
    }

    /** 同 {@link #push(String, String, String)}，复用已有快照。 */
    public static Scope push(RequestContext ctx) {
        final RequestContext prev = HOLDER.get();
        final AtomicBoolean closed = new AtomicBoolean(false);
        HOLDER.set(ctx);
        return () -> {
            if (closed.compareAndSet(false, true)) {
                if (prev == null) {
                    HOLDER.remove();
                } else {
                    HOLDER.set(prev);
                }
            }
        };
    }

    /** 在当前线程请求上下文内执行 runnable，结束后自动还原线程原上下文。 */
    public static void run(RequestContext ctx, Runnable runnable) {
        try (Scope ignored = push(ctx)) {
            runnable.run();
        }
    }

    /** 同 {@link #run}，带返回值。 */
    public static <T> T call(RequestContext ctx, Supplier<T> supplier) {
        try (Scope ignored = push(ctx)) {
            return supplier.get();
        }
    }

    /** 当前线程请求字段（可能为空）。 */
    public static Optional<RequestContext> current() {
        return Optional.ofNullable(HOLDER.get());
    }

    /** 清理当前线程请求字段（测试/长任务/线程池兜底用；正常请求走 {@link Scope#close()} 自动还原）。 */
    public static void clear() {
        HOLDER.remove();
    }

    /** 请求级 community 覆盖值；无覆盖返回 {@link Optional#empty()}。 */
    public static Optional<String> communityOverride() {
        return current().map(ctx -> ctx.community);
    }

    /** 请求级 request_id。 */
    public static Optional<String> currentRequestId() {
        return current().map(ctx -> ctx.requestId);
    }

    /** 请求级 trace_id（预留）。 */
    public static Optional<String> currentTraceId() {
        return current().map(ctx -> ctx.traceId);
    }

    /** 供日志装配读取的字段快照（写入 MDC）。 */
    public Map<String, String> asMdcFields() {
        Map<String, String> fields = new HashMap<>();
        if (community != null) {
            fields.put("community", community);
        }
        if (requestId != null) {
            fields.put("request_id", requestId);
        }
        if (traceId != null) {
            fields.put("trace_id", traceId);
        }
        return fields;
    }

    public String community() {
        return community;
    }

    public String requestId() {
        return requestId;
    }

    public String traceId() {
        return traceId;
    }

    /** 作用域句柄：{@link RequestContext#push} 的返回，close 时还原线程上下文。 */
    @FunctionalInterface
    public interface Scope extends AutoCloseable {
        @Override
        void close();
    }
}
