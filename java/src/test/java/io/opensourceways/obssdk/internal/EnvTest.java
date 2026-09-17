package io.opensourceways.obssdk.internal;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assumptions.assumeTrue;

/**
 * 静态字段三级解析：显式参数 &gt; {@code OBS_*} 环境变量 &gt; 内置默认。
 *
 * <p>这一层此前缺失兜底，导致未注入环境变量时 {@code service}/{@code env}/
 * {@code instance}/{@code community} 四个**必填**字段整个从日志 JSON 里消失
 * （见 spec/common-fields.md）。
 */
class EnvTest {

    /** 必然未设置的键，用于验证「环境变量缺失 → 兜底」这一级。 */
    private static final String UNSET_KEY = "OBS_SDK_TEST_KEY_THAT_IS_NEVER_SET";

    @Test
    void 显式参数优先于环境变量与兜底值() {
        assertEquals("review", Env.resolve("review", UNSET_KEY, "unknown"));
        // 该环境变量确实有值时，显式参数仍优先
        assertEquals("review", Env.resolve("review", "PATH", "unknown"));
    }

    @Test
    void 环境变量缺失时回退兜底值() {
        assertNull(System.getenv(UNSET_KEY), "该键不应被设置，测试前提不成立");
        assertEquals("unknown", Env.resolve(null, UNSET_KEY, "unknown"));
    }

    @Test
    void 空串与null一样视为未设置() {
        // 空串若被当作有效值，日志里就会出现 "service":""，等价于字段缺失
        assertEquals("unknown", Env.resolve("", UNSET_KEY, "unknown"));
        assertEquals("unknown", Env.resolve(null, UNSET_KEY, "unknown"));
    }

    @Test
    void 环境变量存在时优先于兜底值() {
        String path = System.getenv("PATH");
        assumeTrue(path != null && !path.isEmpty(), "PATH 未设置，跳过");
        assertEquals(path, Env.resolve(null, "PATH", "unknown"));
    }

    @Test
    void 四个字段未配置时恒非空() {
        // 契约默认：service/env/community = unknown，instance = hostname。
        // 只要有一个为 null/空，该字段就会从日志与指标里整条消失。
        String[] resolved = {
                Env.service(null), Env.env(null), Env.instance(null), Env.community(null),
        };
        for (String value : resolved) {
            assertNotNull(value);
            assertFalse(value.isEmpty());
        }
    }

    @Test
    void 常量名与契约一致() {
        assertEquals("OBS_SERVICE", Env.ENV_SERVICE);
        assertEquals("OBS_ENV", Env.ENV_ENV);
        assertEquals("OBS_INSTANCE", Env.ENV_INSTANCE);
        assertEquals("OBS_COMMUNITY", Env.ENV_COMMUNITY);
    }
}
