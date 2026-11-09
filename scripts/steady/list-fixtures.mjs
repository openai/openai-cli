import { parse } from "@std/yaml";

// Steady serves one static response per operation. A generated has_more=true
// would promise another page while returning the same rows for every cursor.
const spec = parse(await Deno.readTextFile(Deno.args[0]));
for (const path of Object.values(spec.paths)) {
  const media = path.get?.responses?.["200"]?.content?.["application/json"];
  const schema = media?.schema;
  const reference = schema?.$ref;
  const page = reference?.startsWith("#/components/schemas/")
    ? spec.components.schemas[reference.slice("#/components/schemas/".length)]
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
  if (fields.next?.anyOf?.some((variant) => variant.type === "null")) {
    properties.next = { ...fields.next, default: null };
  }
  media.schema = { ...page, properties };
}
console.log(JSON.stringify(spec));
