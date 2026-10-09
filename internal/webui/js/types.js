// Catalog response shape. Keep required fields aligned with catalog.Candidate.
/**
 * @typedef {Object} Candidate
 * @property {string} title
 * @property {string[]} authors
 * @property {string[]} translators
 * @property {boolean} work_record
 * @property {Array<{field: string, value: string, source: string, url: string}>} evidence
 * @property {string} publisher
 * @property {string} pubdate
 * @property {string} isbn
 * @property {string[]} isbns
 * @property {string[]} languages
 * @property {string} series
 * @property {string[]} series_labels
 * @property {string} series_index
 * @property {string} original_title
 * @property {string} subtitle
 * @property {string} description
 * @property {string} language
 * @property {string[]} categories
 * @property {string} cover
 * @property {string} url
 * @property {string} source
 * @property {number} confidence
 * @property {string} source_notes
 * @property {Object<string, string>} field_sources
 * @property {string} local_title
 * @property {string} local_subtitle
 * @property {string} identifiers
 * @property {string} catalog_url
 * @property {string=} display_title
 * @property {string=} edition
 * @property {string=} collection
 * @property {string=} rating
 * @property {string=} rights
 */
/**
 * @typedef {Object} Book
 * @property {string} id
 * @property {string} path
 * @property {string} name
 * @property {Object<string, *>} metadata
 * @property {Object<string, *>} file_metadata
 * @property {string} search_query
 * @property {boolean} cover_editable
 * @property {boolean} staged
 */
export {};
