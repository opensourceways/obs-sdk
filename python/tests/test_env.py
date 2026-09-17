"""_env 单测：静态字段三级解析（显式参数 > OBS_* > 内置默认）。

这层此前没有测试覆盖，而 Java SDK 的同类缺陷（缺兜底 → 字段落成 null →
日志里整条消失）正是从无覆盖的地方漏到合入后的，所以这里把三级逐级钉住。
"""

import socket

import pytest

from obs_sdk import _env

# 必然未设置的环境变量名，用于验证「环境变量缺失 → 兜底」这一级。
UNSET = "OBS_SDK_TEST_KEY_THAT_IS_NEVER_SET"


def test_explicit_wins_over_env_and_default(monkeypatch):
    # 该环境变量确实有值时，显式参数仍优先。
    monkeypatch.setenv(UNSET, "from-env")
    assert _env._resolve("explicit", UNSET, "unknown") == "explicit"
    assert _env.service("review") == "review"
    assert _env.env("test") == "test"
    assert _env.community("openeuler") == "openeuler"
    assert _env.instance("pod-1") == "pod-1"


def test_env_wins_over_default(monkeypatch):
    monkeypatch.setenv("OBS_SERVICE", "from-env")
    monkeypatch.delenv("OBS_SDK_TEST_KEY", raising=False)
    assert _env.service(None) == "from-env"


def test_empty_string_treated_as_unset(monkeypatch):
    # 空串若被当成有效值，日志里就会出现 "service":""，等价于字段缺失。
    monkeypatch.setenv("OBS_SERVICE", "")
    monkeypatch.setenv(UNSET, "")
    assert _env.service(None) == "unknown"
    assert _env._resolve("", UNSET, "unknown") == "unknown"


def test_falls_back_to_contract_defaults(monkeypatch):
    for key in ("OBS_SERVICE", "OBS_ENV", "OBS_COMMUNITY"):
        monkeypatch.delenv(key, raising=False)
    assert _env.service(None) == "unknown"
    assert _env.env(None) == "unknown"
    assert _env.community(None) == "unknown"


def test_instance_falls_back_to_hostname(monkeypatch):
    monkeypatch.delenv("OBS_INSTANCE", raising=False)
    assert _env.instance(None) == socket.gethostname()
    monkeypatch.setenv("OBS_INSTANCE", "pod-1")
    assert _env.instance(None) == "pod-1"


def test_all_fields_never_empty(monkeypatch):
    # 契约：四个部署级字段取值恒非空。任一为空 → 日志 JSON 里该键整条消失、
    # 指标 label 被跳过，采集侧静默漏数。
    for key in ("OBS_SERVICE", "OBS_ENV", "OBS_INSTANCE", "OBS_COMMUNITY"):
        monkeypatch.delenv(key, raising=False)
    for value in (_env.service(), _env.env(), _env.instance(), _env.community()):
        assert value
