package agent

// DocVersionDetectPrompt is used during document processing to extract
// structured metadata (product model, document type, version, vendor) from
// the opening sections of a document.
//
// Design rationale:
//   - Robustel technical documents are highly structured: cover page and first
//     section always contain model number, document type, and version string.
//   - Feeding only the first few chunks (~cover + TOC) keeps cost negligible
//     (~2-3 s) while achieving near-100% accuracy on this corpus.
//   - The model must return valid JSON and ONLY JSON — no markdown fences,
//     no prose. Strict parsing; any deviation triggers graceful skip.
//
// Placeholders:
//   {{content}}  — concatenated text of the first N chunks of the document.
const DocVersionDetectPrompt = `You are a document metadata extractor. Read the text below (opening sections of a technical document) and extract the following four fields:

- product_model : product model / part number / code name (e.g. "R1511", "EG5120-4G", "RobustOS Pro"). If multiple models are mentioned pick the primary one. Empty string if not found.
- doc_type      : document type (e.g. "Software Manual", "Hardware Manual", "Datasheet", "Quick Start Guide", "Application Note"). Empty string if not found.
- version       : document or software version string (e.g. "1.0.2", "V5.5.0", "2.4"). Use the document version, not a firmware version embedded in an example. Empty string if not found.
- vendor        : manufacturer / vendor name (e.g. "Robustel", "Cisco", "Moxa"). Empty string if not found.

Return ONLY a valid JSON object with exactly these four string keys. Do not wrap in markdown code fences. Do not add any explanation.

Example output:
{"product_model":"R1511","doc_type":"Software Manual","version":"5.5.0","vendor":"Robustel"}

Document text:
{{content}}`
