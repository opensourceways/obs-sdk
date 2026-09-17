package io.opensourceways.obssdk.middleware;

import io.opensourceways.obssdk.ObsSdkConfig;
import io.opensourceways.obssdk.context.RequestContext;
import io.opensourceways.obssdk.log.ObsLogging;
import jakarta.servlet.ServletException;
import jakarta.servlet.http.HttpServletRequest;
import jakarta.servlet.http.HttpServletResponse;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.slf4j.MDC;

import java.lang.reflect.Proxy;
import java.util.HashMap;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * {@link ObsFilter} 行为测试：request_id 注入、community 可信解析与回退、
 * 以及请求结束（含异常穿透）后的作用域清理。
 *
 * <p>其中「请求结束后 community 复位为部署默认」是为一个真实缺陷补的回归测试：
 * MDC 是线程本地的，而 web 容器的请求线程是复用的，若 {@code clearRequestScope()}
 * 漏清 community，上一个请求的覆盖值会串给同一线程后续打出的日志（定时任务 /
 * 异步回调），静默污染按 community 的聚合与告警。
 *
 * <p>用 {@link Proxy} 造 servlet 接口替身，避免为一个测试引入 mock 框架。
 */
class ObsFilterTest {

    private static final String DEFAULT_COMMUNITY = "openeuler";

    @BeforeEach
    void setUp() {
        MDC.clear();
        RequestContext.clear();
        ObsLogging.init(ObsSdkConfig.builder()
                .service("review")
                .env("test")
                .instance("pod-1")
                .community(DEFAULT_COMMUNITY)
                .build());
    }

    @AfterEach
    void tearDown() {
        MDC.clear();
        RequestContext.clear();
    }

    // ---- request_id ----

    @Test
    void 入站X_Request_Id被沿用() throws Exception {
        AtomicReference<String> seen = new AtomicReference<>();
        new ObsFilter().doFilter(request(Map.of(ObsFilter.HEADER_REQUEST_ID, "req-inbound")), response(),
                (req, res) -> seen.set(MDC.get(ObsLogging.MDC_REQUEST_ID)));

        assertEquals("req-inbound", seen.get());
    }

    @Test
    void 无入站请求头时生成request_id() throws Exception {
        AtomicReference<String> seen = new AtomicReference<>();
        new ObsFilter().doFilter(request(Map.of()), response(),
                (req, res) -> seen.set(MDC.get(ObsLogging.MDC_REQUEST_ID)));

        assertNotNull(seen.get());
        assertFalse(seen.get().isEmpty());
    }

    @Test
    void 无入站trace_id时不生成_预留位保持缺席() throws Exception {
        AtomicReference<String> seen = new AtomicReference<>();
        new ObsFilter().doFilter(request(Map.of()), response(),
                (req, res) -> seen.set(MDC.get(ObsLogging.MDC_TRACE_ID)));

        assertNull(seen.get());
    }

    // ---- community 解析与回退 ----

    @Test
    void resolver返回值写入MDC与请求上下文() throws Exception {
        AtomicReference<String> inMdc = new AtomicReference<>();
        AtomicReference<String> inContext = new AtomicReference<>();
        new ObsFilter(req -> "mindspore").doFilter(request(Map.of()), response(), (req, res) -> {
            inMdc.set(MDC.get(ObsLogging.MDC_COMMUNITY));
            inContext.set(RequestContext.communityOverride().orElse(null));
        });

        assertEquals("mindspore", inMdc.get());
        assertEquals("mindspore", inContext.get());
    }

    @Test
    void resolver返回null时回退部署默认() throws Exception {
        AtomicReference<String> seen = new AtomicReference<>();
        new ObsFilter(req -> null).doFilter(request(Map.of()), response(),
                (req, res) -> seen.set(MDC.get(ObsLogging.MDC_COMMUNITY)));

        assertEquals(DEFAULT_COMMUNITY, seen.get());
    }

    @Test
    void 未配置resolver时退化为部署级单社区() throws Exception {
        AtomicReference<String> inMdc = new AtomicReference<>();
        AtomicReference<String> inContext = new AtomicReference<>();
        new ObsFilter().doFilter(request(Map.of()), response(), (req, res) -> {
            inMdc.set(MDC.get(ObsLogging.MDC_COMMUNITY));
            inContext.set(RequestContext.communityOverride().orElse(null));
        });

        assertEquals(DEFAULT_COMMUNITY, inMdc.get());
        // 无覆盖：上下文里不应出现可读到的覆盖值（Optional.empty）
        assertNull(inContext.get());
    }

    // ---- 作用域与 MDC 清理 ----

    @Test
    void 请求结束后community复位为部署默认而非上一请求的覆盖值() throws Exception {
        // 第一个请求：resolver 给出覆盖值
        new ObsFilter(req -> "mindspore").doFilter(request(Map.of()), response(), (req, res) -> {
        });
        assertEquals(DEFAULT_COMMUNITY, MDC.get(ObsLogging.MDC_COMMUNITY),
                "请求结束未复位，覆盖值会留给同一线程的后续日志");

        // 模拟线程复用：第二个请求无覆盖，读到的必须是部署默认而不是 mindspore
        AtomicReference<String> seen = new AtomicReference<>();
        new ObsFilter(req -> null).doFilter(request(Map.of()), response(),
                (req, res) -> seen.set(MDC.get(ObsLogging.MDC_COMMUNITY)));
        assertEquals(DEFAULT_COMMUNITY, seen.get());
    }

    @Test
    void 请求结束后request_id被清掉() throws Exception {
        new ObsFilter().doFilter(request(Map.of(ObsFilter.HEADER_REQUEST_ID, "req-1")), response(),
                (req, res) -> {
                });

        assertNull(MDC.get(ObsLogging.MDC_REQUEST_ID));
    }

    @Test
    void 链路抛异常时作用域与MDC仍被清理() throws Exception {
        ObsFilter filter = new ObsFilter(req -> "mindspore");
        Map<String, String> headers = new HashMap<>();
        headers.put(ObsFilter.HEADER_REQUEST_ID, "req-1");
        headers.put(ObsFilter.HEADER_TRACE_ID, "trace-1");

        assertThrows(ServletException.class, () -> filter.doFilter(request(headers), response(),
                (req, res) -> {
                    throw new ServletException("boom");
                }));

        assertNull(MDC.get(ObsLogging.MDC_REQUEST_ID));
        assertNull(MDC.get(ObsLogging.MDC_TRACE_ID));
        assertNull(MDC.get(ObsLogging.MDC_SPAN_ID));
        assertEquals(DEFAULT_COMMUNITY, MDC.get(ObsLogging.MDC_COMMUNITY));
        assertTrue(RequestContext.current().isEmpty(), "请求上下文未还原");
    }

    // ---- servlet 接口替身 ----

    /** 只实现 {@code getHeader} 的 HttpServletRequest 替身；其余方法返回类型零值。 */
    private static HttpServletRequest request(Map<String, String> headers) {
        return (HttpServletRequest) Proxy.newProxyInstance(
                ObsFilterTest.class.getClassLoader(),
                new Class<?>[]{HttpServletRequest.class},
                (proxy, method, args) -> {
                    if ("getHeader".equals(method.getName())) {
                        return headers.get((String) args[0]);
                    }
                    return zeroValue(method.getReturnType());
                });
    }

    private static HttpServletResponse response() {
        return (HttpServletResponse) Proxy.newProxyInstance(
                ObsFilterTest.class.getClassLoader(),
                new Class<?>[]{HttpServletResponse.class},
                (proxy, method, args) -> zeroValue(method.getReturnType()));
    }

    /** Proxy 的 InvocationHandler 对未处理方法必须返回合法值：基本类型不能给 null。 */
    private static Object zeroValue(Class<?> type) {
        if (!type.isPrimitive() || type == void.class) {
            return null;
        }
        if (type == boolean.class) {
            return false;
        }
        if (type == char.class) {
            return (char) 0;
        }
        if (type == long.class) {
            return 0L;
        }
        if (type == float.class) {
            return 0f;
        }
        if (type == double.class) {
            return 0d;
        }
        return 0;
    }
}
