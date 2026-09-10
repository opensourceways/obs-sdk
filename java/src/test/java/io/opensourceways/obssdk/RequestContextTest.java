package io.opensourceways.obssdk;

import io.opensourceways.obssdk.context.RequestContext;
import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

class RequestContextTest {

    @Test
    void push之后读到覆盖值_close后还原为空() {
        assertFalse(RequestContext.communityOverride().isPresent());

        try (RequestContext.Scope s = RequestContext.push("openeuler", "req-1", "trace-1")) {
            assertEquals("openeuler", RequestContext.communityOverride().orElse(null));
            assertEquals("req-1", RequestContext.currentRequestId().orElse(null));
            assertEquals("trace-1", RequestContext.currentTraceId().orElse(null));
        }

        assertFalse(RequestContext.communityOverride().isPresent());
        assertFalse(RequestContext.currentRequestId().isPresent());
    }

    @Test
    void 嵌套push_内层关闭后还原外层() {
        try (RequestContext.Scope outer = RequestContext.push("openeuler", "r-outer", null)) {
            try (RequestContext.Scope inner = RequestContext.push("mindspore", "r-inner", null)) {
                assertEquals("mindspore", RequestContext.communityOverride().orElse(null));
            }
            assertEquals("openeuler", RequestContext.communityOverride().orElse(null));
            assertEquals("r-outer", RequestContext.currentRequestId().orElse(null));
        }
        assertFalse(RequestContext.communityOverride().isPresent());
    }

    @Test
    void call返回结果并还原上下文() {
        try (RequestContext.Scope s = RequestContext.push("openeuler", null, null)) {
            String result = RequestContext.call(
                    RequestContext.of("mindspore", "r-1", null),
                    () -> RequestContext.communityOverride().orElse(null));
            assertEquals("mindspore", result);
            // call 结束应还原到外层
            assertEquals("openeuler", RequestContext.communityOverride().orElse(null));
        }
    }

    @Test
    void 无请求上下文时无覆盖() {
        RequestContext.clear();
        assertTrue(RequestContext.current().isEmpty());
        assertFalse(RequestContext.communityOverride().isPresent());
    }

    @Test
    void span_id预留_四参push可读且进MDC快照() {
        RequestContext.clear();
        assertFalse(RequestContext.currentSpanId().isPresent());

        try (RequestContext.Scope s = RequestContext.push("openeuler", "req-1", "trace-1", "span-abc")) {
            assertEquals("span-abc", RequestContext.currentSpanId().orElse(null));
            assertEquals("trace-1", RequestContext.currentTraceId().orElse(null));
            assertEquals("span-abc",
                    RequestContext.current().orElseThrow().asMdcFields().get("span_id"));
        }
        assertFalse(RequestContext.currentSpanId().isPresent());

        // 三参重载（首期用法）：span_id 缺省，不进 MDC 快照。
        try (RequestContext.Scope s = RequestContext.push("openeuler", "req-1", "trace-1")) {
            assertFalse(RequestContext.currentSpanId().isPresent());
            assertFalse(RequestContext.current().orElseThrow().asMdcFields().containsKey("span_id"));
            // 其它字段不受影响。
            assertEquals("req-1", RequestContext.currentRequestId().orElse(null));
        }
    }
}
