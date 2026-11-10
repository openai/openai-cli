import { parse } from "@std/yaml";

// Steady serves one static response per operation. A generated has_more=true
// would promise another page while returning the same rows for every cursor.
const spec = parse(await Deno.readTextFile(Deno.args[0]));
for (const path of Object.values(spec.paths)) {
  const media = path.get?.responses?.["200"]?.content?.["application/json"];
  const { $ref: reference, ...schema } = media?.schema ?? {};
  const page = reference?.startsWith("#/components/schemas/")
    ? {
      ...spec.components
        .schemas[reference.slice("#/components/schemas/".length)],
      ...schema,
    }
    : schema;
  const fields = page?.properties;
  if (fields?.data?.type !== "array" || fields?.has_more?.type !== "boolean") {
    continue;
  }

  // Set examples on this response without changing a shared component schema.
  const properties = {
    ...fields,
    has_more: { ...fields.has_more, example: false },
  };
  for (const name of ["next", "next_page"]) {
    const cursor = fields[name];
    if (
      cursor?.nullable === true ||
      (Array.isArray(cursor?.type) && cursor.type.includes("null")) ||
      cursor?.anyOf?.some((variant) => variant.type === "null")
    ) {
      // Steady tries explicit examples before the default.
      properties[name] = {
        ...cursor,
        example: undefined,
        examples: undefined,
        default: null,
      };
    }
  }
  media.schema = { ...page, properties };
}
console.log(JSON.stringify(spec));
