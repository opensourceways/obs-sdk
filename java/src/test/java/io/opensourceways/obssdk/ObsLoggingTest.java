package io.opensourceways.obssdk;

import io.opensourceways.obssdk.context.RequestContext;
import io.opensourceways.obssdk.log.ObsLogging;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.slf4j.MDC;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNull;

/** 日志装配：请求级字段写入 MDC（由 logback JSON encoder 输出为单行 JSON）。 */
class ObsLoggingTest {

    @BeforeEach
    void setUp() {
        ObsLogging.init(ObsSdkConfig.builder()
                .service("review")
                .env("test")
                .instance("pod-1")
                .community("openeuler")
                .build());
    }

    @AfterEach
    void tearDown() {
        ObsLogging.clearRequestScope();
        RequestContext.clear();
        MDC.clear();
    }

    @Test
    void enrich写入请求级字段到MDC_含span_id() {
        try (RequestContext.Scope s = RequestContext.push("mindspore", "req-1", "trace-1", "span-abc")) {
            ObsLogging.enrich(RequestContext.current().orElse(null));

            assertEquals("mindspore", MDC.get(ObsLogging.MDC_COMMUNITY));
            assertEquals("req-1", MDC.get(ObsLogging.MDC_REQUEST_ID));
            assertEquals("trace-1", MDC.get(ObsLogging.MDC_TRACE_ID));
            assertEquals("span-abc", MDC.get(ObsLogging.MDC_SPAN_ID));
        }
    }

    @Test
    void 未提供span_id时不写入MDC() {
        try (RequestContext.Scope s = RequestContext.push("openeuler", "req-1", "trace-1")) {
            ObsLogging.enrich(RequestContext.current().orElse(null));

            assertEquals("trace-1", MDC.get(ObsLogging.MDC_TRACE_ID));
            assertNull(MDC.get(ObsLogging.MDC_SPAN_ID));
        }
    }

    @Test
    void clearRequestScope清掉全部预留位() {
        try (RequestContext.Scope s = RequestContext.push("openeuler", "req-1", "trace-1", "span-abc")) {
            ObsLogging.enrich(RequestContext.current().orElse(null));
            ObsLogging.clearRequestScope();

            assertNull(MDC.get(ObsLogging.MDC_REQUEST_ID));
            assertNull(MDC.get(ObsLogging.MDC_TRACE_ID));
            assertNull(MDC.get(ObsLogging.MDC_SPAN_ID));
        }
    }

    /**
     * 回归：{@code clearRequestScope} 曾漏清 community。
     *
     * <p>MDC 是线程本地的，web 容器线程复用 —— 留着上一个请求的覆盖值会串给同一线程
     * 后续打出的日志（定时任务 / 异步回调），且不报错。注意既要复位成部署默认，
     * 也不能删掉（删了字段会整个从 JSON 里消失）。
     */
    @Test
    void clearRequestScope把community复位为部署默认() {
        try (RequestContext.Scope s = RequestContext.push("mindspore", "req-1", "trace-1")) {
            ObsLogging.enrich(RequestContext.current().orElse(null));
            assertEquals("mindspore", MDC.get(ObsLogging.MDC_COMMUNITY));

            ObsLogging.clearRequestScope();
            assertEquals("openeuler", MDC.get(ObsLogging.MDC_COMMUNITY),
                    "应复位为部署默认，而不是残留上一个请求的覆盖值");
        }
    }

    @Test
    void 部署级静态访问器返回init登记的值() {
        assertEquals("review", ObsLogging.deploymentService());
        assertEquals("test", ObsLogging.deploymentEnv());
        assertEquals("pod-1", ObsLogging.deploymentInstance());
        assertEquals("openeuler", ObsLogging.deploymentCommunity());
    }

    /**
     * 缺陷回归：部署级字段一旦只存在于线程本地 MDC 就会随线程消失；访问器是全局静态
     * 取值，{@code init} 未调用时仍须回退契约默认（"unknown"），保证 provider 恒能
     * 输出非空部署级字段。
     */
    @Test
    void 未init时部署级访问器回退契约默认() throws Exception {
        java.lang.reflect.Field cfgField = ObsLogging.class.getDeclaredField("cfg");
        cfgField.setAccessible(true);
        cfgField.set(null, null);

        assertEquals("unknown", ObsLogging.deploymentService());
        assertEquals("unknown", ObsLogging.deploymentEnv());
        assertEquals("unknown", ObsLogging.deploymentInstance());
        assertEquals("unknown", ObsLogging.deploymentCommunity());
    }
}
