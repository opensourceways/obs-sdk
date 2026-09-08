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
            assertEquals("req-1", RequestContext.requestId().orElse(null));
            assertEquals("trace-1", RequestContext.traceId().orElse(null));
        }

        assertFalse(RequestContext.communityOverride().isPresent());
        assertFalse(RequestContext.requestId().isPresent());
    }

    @Test
    void 嵌套push_内层关闭后还原外层() {
        try (RequestContext.Scope outer = RequestContext.push("openeuler", "r-outer", null)) {
            try (RequestContext.Scope inner = RequestContext.push("mindspore", "r-inner", null)) {
                assertEquals("mindspore", RequestContext.communityOverride().orElse(null));
            }
            assertEquals("openeuler", RequestContext.communityOverride().orElse(null));
            assertEquals("r-outer", RequestContext.requestId().orElse(null));
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
}
