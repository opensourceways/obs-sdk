package io.opensourceways.obssdk;

import ch.qos.logback.classic.LoggerContext;
import ch.qos.logback.classic.joran.JoranConfigurator;
import io.opensourceways.obssdk.log.ObsLogging;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.slf4j.MDC;

import java.io.ByteArrayOutputStream;
import java.io.File;
import java.io.PrintStream;
import java.nio.charset.StandardCharsets;
import java.util.regex.Pattern;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

/**
 * 直接加载接入方会拷走的 {@code examples/logback-json.xml}，断言实际输出符合
 * spec/log-format.md 的契约（字段名/取值/顺序）。
 *
 * <p>这一层必须真跑 encoder 而不是只断言 MDC —— 此前的问题正是在 MDC 之后：
 * 字段名是 {@code @timestamp}/{@code message}、级别是大写、时间为本地时区纳秒，
 * 且未配 {@code <stackTrace/>} 导致 throwable 被整条丢弃。
 */
class ObsJsonProviderTest {

    /** 契约：固定毫秒精度、UTC、零偏移输出 Z。 */
    private static final Pattern TIME_PATTERN =
            Pattern.compile("^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}\\.\\d{3}Z$");

    private static final String[] CONTRACT_ORDER = {
            "time", "level", "msg", "service", "env", "instance", "community",
            "request_id", "trace_id", "span_id", "logger", "error",
    };

    private LoggerContext context;
    private ByteArrayOutputStream captured;
    private PrintStream originalOut;

    @BeforeEach
    void setUp() throws Exception {
        originalOut = System.out;
        captured = new ByteArrayOutputStream();
        // ConsoleAppender 在 start() 时解析 System.out，故必须在配置前替换。
        System.setOut(new PrintStream(captured, true, StandardCharsets.UTF_8));

        File config = new File("examples/logback-json.xml");
        assertTrue(config.isFile(), "样例配置不存在（测试工作目录应为 java/）：" + config.getAbsolutePath());

        context = (LoggerContext) LoggerFactory.getILoggerFactory();
        context.reset();
        JoranConfigurator configurator = new JoranConfigurator();
        configurator.setContext(context);
        configurator.doConfigure(config);

        MDC.clear();
        ObsLogging.init(ObsSdkConfig.builder()
                .service("review")
                .env("test")
                .instance("pod-1")
                .community("openEuler")
                .build());
    }

    @AfterEach
    void tearDown() {
        System.out.flush();
        System.setOut(originalOut);
        MDC.clear();
        context.reset();
    }

    @Test
    void 固定字段名与顺序符合契约_不含encoder默认字段() {
        Logger log = LoggerFactory.getLogger("com.x.ReviewSvc");
        log.info("job done");

        String json = lastLine();

        // encoder 的默认字段名必须全部消失，改用契约名。
        assertFalse(json.contains("@timestamp"), json);
        assertFalse(json.contains("@version"), json);
        assertFalse(json.contains("logger_name"), json);
        assertFalse(json.contains("\"message\""), json);

        assertTrue(json.contains("\"msg\":\"job done\""), json);

        // 固定字段的出现顺序与契约一致。
        int previous = -1;
        for (String key : CONTRACT_ORDER) {
            int index = json.indexOf("\"" + key + "\":");
            if (index < 0) {
                continue; // 可选字段（trace_id/span_id/logger 等）未出现时跳过
            }
            assertTrue(index > previous, "字段 " + key + " 顺序不符契约：" + json);
            previous = index;
        }
    }

    @Test
    void 时间为固定毫秒UTC且以Z结尾() {
        LoggerFactory.getLogger("com.x.ReviewSvc").info("job done");

        String json = lastLine();
        String time = fieldValue(json, "time");

        assertTrue(TIME_PATTERN.matcher(time).matches(), "时间格式不符：" + time);
        assertFalse(time.contains("+"), "应为 UTC 零偏移（Z）而非带偏移：" + time);
    }

    @Test
    void 级别为小写() {
        Logger log = LoggerFactory.getLogger("com.x.ReviewSvc");
        log.warn("watch out");
        log.error("failed");

        String warn = line(0);
        String error = line(1);

        assertTrue(warn.contains("\"level\":\"warn\""), warn);
        assertTrue(error.contains("\"level\":\"error\""), error);
        assertFalse(warn.contains("WARN"), warn);
        assertFalse(error.contains("ERROR"), error);
    }

    @Test
    void logger为调用位置而非logger名() {
        LoggerFactory.getLogger("com.x.ReviewSvc").info("job done");

        String json = lastLine();

        assertTrue(json.contains("\"logger\":\"ObsJsonProviderTest.java:"), json);
        assertFalse(json.contains("com.x.ReviewSvc"), "logger 不应是 logger 名：" + json);
    }

    @Test
    void 请求级字段来自MDC_未设置的预留位不出现() {
        MDC.put("request_id", "req-1");
        LoggerFactory.getLogger("com.x.ReviewSvc").info("scoped");

        String json = lastLine();

        assertTrue(json.contains("\"service\":\"review\""), json);
        assertTrue(json.contains("\"env\":\"test\""), json);
        assertTrue(json.contains("\"instance\":\"pod-1\""), json);
        assertTrue(json.contains("\"community\":\"openEuler\""), json);
        assertTrue(json.contains("\"request_id\":\"req-1\""), json);

        // 二期预留位：未写入 MDC 时不出现在输出里。
        assertFalse(json.contains("trace_id"), json);
        assertFalse(json.contains("span_id"), json);
    }

    @Test
    void 异常写入error字段且保留完整堆栈() {
        LoggerFactory.getLogger("com.x.ReviewSvc")
                .error("get account failed", new IllegalStateException("connection refused"));

        String json = lastLine();

        assertTrue(json.contains("\"error\":\""), "throwable 被丢弃了：" + json);
        assertTrue(json.contains("java.lang.IllegalStateException"), json);
        assertTrue(json.contains("connection refused"), json);
        // 堆栈是多行文本，JSON 转义为 \n，整条仍是单行。
        assertTrue(json.contains("\\n\\tat "), json);
        assertFalse(json.contains("\n"), "输出必须是单行 JSON");
    }

    @Test
    void 无异常时不输出error字段() {
        LoggerFactory.getLogger("com.x.ReviewSvc").info("job done");

        assertFalse(lastLine().contains("\"error\""), lastLine());
    }

    private String line(int index) {
        String[] lines = captured.toString(StandardCharsets.UTF_8).split("\n");
        assertTrue(lines.length > index, "日志行数不足：" + captured);
        return lines[index];
    }

    private String lastLine() {
        String[] lines = captured.toString(StandardCharsets.UTF_8).split("\n");
        assertTrue(lines.length > 0, "没有任何日志输出");
        return lines[lines.length - 1];
    }

    /** 取出 {"key":"value"} 的 value（仅适用于单行 JSON 里的字符串字段）。 */
    private static String fieldValue(String json, String key) {
        String marker = "\"" + key + "\":\"";
        int start = json.indexOf(marker);
        assertTrue(start >= 0, "字段不存在：" + key + " in " + json);
        start += marker.length();
        int end = json.indexOf('"', start);
        return json.substring(start, end);
    }
}
