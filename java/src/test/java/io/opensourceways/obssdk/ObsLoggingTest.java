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
}
