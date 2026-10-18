"""Supported native plugin, installed only in an AO-owned candidate profile.

Hermes pre_llm_call context is user-role context. It preserves the cached native
system prompt; it does not establish a system/developer provider wire role.
"""
from pathlib import Path


def register(ctx):
    def before(*, platform="", **kwargs):
        if platform != "acp":
            return None
        with (Path(__file__).parent / "instructions.txt").open("rb") as stream:
            raw = stream.read(8193)
        if len(raw) > 8192:
            raise ValueError("AO standing context exceeds qualified bound")
        return {"context": raw.decode("utf-8")}

    ctx.register_hook("pre_llm_call", before)
