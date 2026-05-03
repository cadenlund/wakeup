#!/usr/bin/env python3
"""
Minimal Swagger 2.0 → OpenAPI 3.0 converter for the swag-generated spec.

`swag init` produces Swagger 2.0; oapi-codegen v2 (the §13.10 client
generator) only consumes OpenAPI 3.x. Rather than pull in a Node
toolchain (swagger2openapi) just for this one conversion, this script
performs the subset of the v2→v3 mechanical translation we actually
need: parameter `type` → `schema.type`, top-level `definitions` →
`components.schemas`, `host`/`basePath`/`schemes` → `servers`, and
`securityDefinitions` → `components.securitySchemes`.

Drops swag-emitted curly quotes around `example:` values that survive
into v3 as invalid JSON. Stable enough for the gen-client wiring;
not a general v2→v3 tool.

Usage: swagger2-to-openapi3.py <swagger2.json> <openapi3.json>
"""

from __future__ import annotations

import copy
import json
import sys


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: swagger2-to-openapi3.py <input> <output>", file=sys.stderr)
        return 2
    src, dst = sys.argv[1], sys.argv[2]
    with open(src) as f:
        v2 = json.load(f)

    v3: dict = {
        "openapi": "3.0.3",
        "info": v2.get("info", {"title": "API", "version": "0.0"}),
    }

    # host + basePath + schemes → servers (best-effort).
    host = v2.get("host", "")
    base = v2.get("basePath", "")
    schemes = v2.get("schemes") or ["https"]
    if host:
        v3["servers"] = [{"url": f"{s}://{host}{base}"} for s in schemes]

    # Security: securityDefinitions → components.securitySchemes.
    components: dict = {"schemas": {}}
    if defs := v2.get("securityDefinitions"):
        components["securitySchemes"] = defs
    if top_schemas := v2.get("definitions"):
        components["schemas"] = {k: convert_schema(v) for k, v in top_schemas.items()}

    paths = v2.get("paths", {})
    v3["paths"] = {p: convert_path(item) for p, item in paths.items()}

    if components.get("schemas") or components.get("securitySchemes"):
        v3["components"] = components

    if security := v2.get("security"):
        v3["security"] = security

    with open(dst, "w") as f:
        json.dump(v3, f, indent=2)
    return 0


SCHEMA_PROPS = {"type", "format", "items", "properties", "enum", "default"}


def convert_schema(s):
    """Walk a schema-shaped object and rewrite $ref + nested types."""
    if not isinstance(s, dict):
        return s
    out = copy.deepcopy(s)
    if "$ref" in out:
        out["$ref"] = out["$ref"].replace("#/definitions/", "#/components/schemas/")
    if "items" in out:
        out["items"] = convert_schema(out["items"])
    if "properties" in out:
        out["properties"] = {k: convert_schema(v) for k, v in out["properties"].items()}
    if "additionalProperties" in out and isinstance(out["additionalProperties"], dict):
        out["additionalProperties"] = convert_schema(out["additionalProperties"])
    return out


def convert_path(item: dict) -> dict:
    out: dict = {}
    for method, op in item.items():
        if not isinstance(op, dict):
            out[method] = op
            continue
        out[method] = convert_operation(op)
    return out


def convert_operation(op: dict) -> dict:
    out = {k: v for k, v in op.items() if k not in {"parameters", "responses", "consumes", "produces"}}
    body_param = None
    form_data_params: list = []
    params: list = []
    for p in op.get("parameters", []) or []:
        if p.get("in") == "body":
            body_param = p
            continue
        if p.get("in") == "formData":
            form_data_params.append(p)
            continue
        # v3 puts type under schema.
        new_p = {k: v for k, v in p.items() if k not in {"type", "format", "items", "enum", "default"}}
        schema: dict = {}
        for k in ("type", "format", "items", "enum", "default"):
            if k in p:
                schema[k] = p[k]
        if schema:
            if "items" in schema:
                schema["items"] = convert_schema(schema["items"])
            new_p["schema"] = schema
        # v2 example may include literal quotes that survived swag's
        # parsing — strip them so the v3 example is valid JSON.
        if isinstance(new_p.get("example"), str):
            ex = new_p["example"]
            if len(ex) >= 2 and ex.startswith('"') and ex.endswith('"'):
                new_p["example"] = ex[1:-1]
        params.append(new_p)
    if params:
        out["parameters"] = params

    if body_param is not None:
        body_schema = body_param.get("schema") or {}
        out["requestBody"] = {
            "required": body_param.get("required", False),
            "content": {
                ct: {"schema": convert_schema(body_schema)}
                for ct in op.get("consumes") or ["application/json"]
            },
        }
    elif form_data_params:
        # v2 formData params collapse into a single multipart/form-data
        # requestBody in v3. `type: file` becomes string/binary.
        properties: dict = {}
        required: list = []
        for fp in form_data_params:
            t = fp.get("type", "string")
            if t == "file":
                properties[fp["name"]] = {"type": "string", "format": "binary"}
            else:
                properties[fp["name"]] = {"type": t}
            if fp.get("description"):
                properties[fp["name"]]["description"] = fp["description"]
            if fp.get("required"):
                required.append(fp["name"])
        schema = {"type": "object", "properties": properties}
        if required:
            schema["required"] = required
        out["requestBody"] = {
            "required": bool(required),
            "content": {"multipart/form-data": {"schema": schema}},
        }

    out["responses"] = {
        code: convert_response(resp, op.get("produces") or ["application/json"])
        for code, resp in (op.get("responses") or {}).items()
    }
    return out


def convert_response(resp: dict, produces: list) -> dict:
    if not isinstance(resp, dict):
        return resp
    out: dict = {"description": resp.get("description", "")}
    if schema := resp.get("schema"):
        out["content"] = {
            ct: {"schema": convert_schema(schema)} for ct in produces
        }
    if headers := resp.get("headers"):
        out["headers"] = {
            name: {"description": h.get("description", ""),
                   "schema": {k: v for k, v in h.items() if k in SCHEMA_PROPS}}
            for name, h in headers.items()
        }
    return out


if __name__ == "__main__":
    sys.exit(main())
