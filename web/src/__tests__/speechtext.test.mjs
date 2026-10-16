// The text a Piper voice is given (speechText.ts): abbreviations written out, numbers in words and
// the pauses made explicit, in Spanish and in English, and how a sentence is kept whole around an
// abbreviation (speakable.ts).
import test from "node:test";
import assert from "node:assert/strict";

const m = await import("../speechText.ts");
const speakable = await import("../speakable.ts");

const said = (text, lang) => m.speechText(text, lang);

test("Spanish abbreviations are written out, keeping the capital they had", () => {
  assert.equal(
    said("El Sr. García y la Sra. Ruiz hablan con la Srta. Pérez", "es"),
    "El Señor García y la Señora Ruiz hablan con la Señorita Pérez.",
  );
  assert.equal(
    said("El Dr. Soto y la dra. Mena, los Sres. Díaz", "es-MX"),
    "El Doctor Soto y la doctora Mena, los Señores Díaz.",
  );
  assert.equal(
    said("El Lic. Vera, el Ing. Paz, el Arq. Gil y el Prof. Cruz", "es"),
    "El Licenciado Vera, el Ingeniero Paz, el Arquitecto Gil y el Profesor Cruz.",
  );
  assert.equal(
    said("Gral. Soto en la Av. Mayor, véase pág. 4 y págs. 5", "es"),
    "General Soto en la Avenida Mayor, véase página cuatro y páginas cinco.",
  );
  assert.equal(
    said("Ud. y uds. llaman al tel. 5, núm. 7, aprox. dos, vs. ellos", "es"),
    "Usted y ustedes llaman al teléfono cinco, número siete, aproximadamente dos, versus ellos.",
  );
  assert.equal(
    said("Es útil, p. ej. así, o p.ej. asá", "es"),
    "Es útil, por ejemplo así, o por ejemplo asá.",
  );
});

test("an abbreviation that can end a sentence keeps its full stop when another follows", () => {
  assert.equal(
    said("Frutas, verduras, etc. Después vamos.", "es"),
    "Frutas, verduras, etcétera. Después vamos.",
  );
  assert.equal(
    said("Frutas, verduras, etc.", "es"),
    "Frutas, verduras, etcétera.",
  );
  assert.equal(
    said("Frutas, etc. y más", "es"),
    "Frutas, etcétera y más.",
    "a lower-case word goes on in the same sentence",
  );
  assert.equal(
    said("Viven en EE. UU. Y también en EE.UU.", "es"),
    "Viven en Estados Unidos. Y también en Estados Unidos.",
  );
});

test("English abbreviations are written out", () => {
  assert.equal(
    said("Mr. Smith, Mrs. Jones and Ms. Lee met Dr. Who", "en"),
    "Mister Smith, Missus Jones and Miss Lee met Doctor Who.",
  );
  assert.equal(
    said("Prof. Xu, John Smith Jr. and Sr. Mt. Hood, see fig. 2", "en"),
    "Professor Xu, John Smith Junior and Senior Mount Hood, see figure two.",
  );
  assert.equal(
    said("About approx. five, i.e. a few, e.g. this, vs. that", "en"),
    "About approximately five, that is a few, for example this, versus that.",
  );
  assert.equal(
    said("It was 10 a.m. Then 5 p.m. Acme Inc. Ltd. U.S. Now", "en"),
    "It was ten A M. Then five P M. Acme Incorporated. Limited. U S. Now.",
  );
  assert.equal(said("Lists, etc.", "en"), "Lists, et cetera.");
});

test("an abbreviation inside a word is left alone", () => {
  assert.equal(said("El sr.x y la ing.", "es"), "El sr.x y la ingeniero.");
  assert.equal(said("El Dr.Who", "en"), "El Dr.Who.");
});

test("Spanish numbers are written in words", () => {
  const es = (digits, before) => m.numberWords(digits, "es", before);
  const cases = {
    0: "cero",
    1: "uno",
    9: "nueve",
    10: "diez",
    15: "quince",
    16: "dieciséis",
    20: "veinte",
    21: "veintiuno",
    22: "veintidós",
    29: "veintinueve",
    30: "treinta",
    31: "treinta y uno",
    45: "cuarenta y cinco",
    99: "noventa y nueve",
    100: "cien",
    101: "ciento uno",
    115: "ciento quince",
    200: "doscientos",
    999: "novecientos noventa y nueve",
    1000: "mil",
    1001: "mil uno",
    1999: "mil novecientos noventa y nueve",
    2000: "dos mil",
    21000: "veintiún mil",
    31000: "treinta y un mil",
    100000: "cien mil",
    101000: "ciento un mil",
    999999: "novecientos noventa y nueve mil novecientos noventa y nueve",
    1000000: "un millón",
    1000001: "un millón uno",
    2000000: "dos millones",
    21000000: "veintiún millones",
    100000000: "cien millones",
    1000000000: "mil millones",
    2000000000: "dos mil millones",
    999999999999:
      "novecientos noventa y nueve mil novecientos noventa y nueve millones novecientos noventa y nueve mil novecientos noventa y nueve",
  };
  for (const [digits, words] of Object.entries(cases)) {
    assert.equal(es(digits), words, digits);
  }
  // Before a noun a one is "un".
  assert.equal(es("1", true), "un");
  assert.equal(es("21", true), "veintiún");
  assert.equal(es("31", true), "treinta y un");
  assert.equal(es("101", true), "ciento un");
  assert.equal(es("12", true), "doce");
  assert.equal(es("100", true), "cien");
});

test("English numbers are written in words", () => {
  const en = (digits) => m.numberWords(digits, "en");
  const cases = {
    0: "zero",
    1: "one",
    13: "thirteen",
    19: "nineteen",
    20: "twenty",
    21: "twenty-one",
    99: "ninety-nine",
    100: "one hundred",
    101: "one hundred one",
    342: "three hundred forty-two",
    1000: "one thousand",
    2024: "two thousand twenty-four",
    10000: "ten thousand",
    100001: "one hundred thousand one",
    1000000: "one million",
    1200300: "one million two hundred thousand three hundred",
    2000000000: "two billion",
    999999999999:
      "nine hundred ninety-nine billion nine hundred ninety-nine million nine hundred ninety-nine thousand nine hundred ninety-nine",
  };
  for (const [digits, words] of Object.entries(cases)) {
    assert.equal(en(digits), words, digits);
  }
});

test("a number with leading zeros, or too long, is read digit by digit", () => {
  assert.equal(m.numberWords("007", "es"), "cero cero siete");
  assert.equal(m.numberWords("0", "es"), "cero");
  assert.equal(m.numberWords("05", "en"), "zero five");
  assert.equal(
    m.numberWords("1234567890123", "en"),
    "one two three four five six seven eight nine zero one two three",
  );
});

test("numbers in a Spanish sentence: decimals, thousands, signs and percentages", () => {
  assert.equal(
    said("Tengo 25 años y 3,14 pesos", "es"),
    "Tengo veinticinco años y tres coma uno cuatro pesos.",
  );
  assert.equal(
    said("Son 1.234.567 personas y 1.000 casas", "es"),
    "Son un millón doscientos treinta y cuatro mil quinientos sesenta y siete personas y mil casas.",
  );
  assert.equal(
    said("Cuesta 1.234,5 en total", "es"),
    "Cuesta mil doscientos treinta y cuatro coma cinco en total.",
  );
  assert.equal(
    said("Subió el 45% y bajó -5 grados", "es"),
    "Subió el cuarenta y cinco por ciento y bajó menos cinco grados.",
  );
  assert.equal(said("Mide 3.5 metros", "es"), "Mide tres coma cinco metros.");
  assert.equal(
    said("Hay 21 casas y 31 perros", "es"),
    "Hay veintiún casas y treinta y un perros.",
    "a one before a noun is un",
  );
  assert.equal(
    said("El 1 de mayo, los 21 y 1.", "es"),
    "El uno de mayo, los veintiuno y uno.",
    "not before de, y or the end",
  );
  assert.equal(
    said("El 1% de 21%", "es"),
    "El uno por ciento de veintiuno por ciento.",
    "a percentage is not counting a noun",
  );
});

test("numbers in an English sentence: decimals, thousands, ordinals and times", () => {
  assert.equal(
    said("I paid 1,250.75 for 3 of them, 25% off", "en"),
    "I paid one thousand two hundred fifty point seven five for three of them, twenty-five percent off.",
  );
  assert.equal(
    said("1,000,000 and 2024", "en"),
    "one million and two thousand twenty-four.",
  );
  assert.equal(said("It is -5 outside", "en"), "It is minus five outside.");
  assert.equal(
    said("On the 1st, 2nd, 3rd, 4th, 12th, 20th, 21st and 100th", "en"),
    "On the first, second, third, fourth, twelfth, twentieth, twenty-first and one hundredth.",
  );
  assert.equal(
    said("At 10:30 or 9:05, or at 7:00", "en"),
    "At ten thirty or nine zero five, or at seven.",
  );
  assert.equal(
    said("A 5th and 8th and 9th and 5 one", "en"),
    "A fifth and eighth and ninth and five one.",
  );
});

test("what is not a number is left as it was", () => {
  assert.equal(
    said("Versión 1.2.3 de x86 y abc123", "es"),
    "Versión 1.2.3 de x86 y abc123.",
  );
  assert.equal(said("Version 1,2,3", "en"), "Version 1,2,3.");
  assert.equal(said("Pi is 1.5.2", "en"), "Pi is 1.5.2.");
  assert.equal(said("Mixed 1.2,3.4", "es"), "Mixed 1.2,3.4.");
  assert.equal(said("Mixed 1,2.3,4", "en"), "Mixed 1,2.3,4.");
  assert.equal(said("Código 12.34.567", "es"), "Código 12.34.567.");
  assert.equal(said("Código 0.123", "en"), "Código zero point one two three.");
  assert.equal(said("Dato 0.123.456", "es"), "Dato 0.123.456.");
  assert.equal(said("Año 2000.123.456,5", "es"), "Año 2000.123.456,5.");
  assert.equal(
    said("COVID-19 and 10-20", "en"),
    "COVID-nineteen and ten-twenty.",
  );
});

test("the pauses: a comma is short, a semicolon medium, a full stop long, an ellipsis dramatic", () => {
  assert.equal(
    said("Hola, ¿cómo estás? ... Me alegra mucho verte", "es"),
    "Hola, ¿cómo estás?... Me alegra mucho verte.",
  );
  assert.equal(said("Uno; dos, tres", "es"), "Uno; dos, tres.");
  assert.equal(said("Espera…", "es"), "Espera...");
  assert.equal(said("Espera . . . ya", "es"), "Espera... ya.");
  assert.equal(said("Espera.....", "es"), "Espera...");
  assert.equal(said("¿De verdad?", "es"), "¿De verdad?");
  assert.equal(said("¡Basta!", "es"), "¡Basta!");
});

test("dashes, colons, brackets, slashes and quotes become pauses or go", () => {
  assert.equal(
    said("Mira - dijo ella — y se fue", "es"),
    "Mira, dijo ella, y se fue.",
  );
  assert.equal(said("Una cosa: dos cosas", "es"), "Una cosa, dos cosas.");
  assert.equal(said("Fin:", "es"), "Fin.");
  assert.equal(said("Fin;", "es"), "Fin.");
  assert.equal(said("Fin,", "es"), "Fin.");
  assert.equal(
    said("El gato (el negro) duerme [mucho] {hoy}", "es"),
    "El gato, el negro, duerme, mucho, hoy.",
  );
  assert.equal(said("rojo / azul", "es"), "rojo, azul.");
  assert.equal(said("y/o", "es"), "y/o.");
  assert.equal(
    said('Dijo «hola», “adiós” y "gracias"', "es"),
    "Dijo hola, adiós y gracias.",
  );
  assert.equal(said("It’s ‘fine’", "en"), "It's 'fine'.");
  assert.equal(said("(Nota) texto", "es"), "Nota, texto.");
  assert.equal(said("a ,; b ;, c", "es"), "a; b; c.");
});

test("lines become pauses, and spaces are tidied", () => {
  assert.equal(
    said("Uno\ndos\n\ntres. \ncuatro,\ncinco", "es"),
    "Uno. dos. tres. cuatro, cinco.",
  );
  assert.equal(said("  mucho   espacio  ", "es"), "mucho espacio.");
  assert.equal(said("Palabra .", "es"), "Palabra.");
});

test("a sentence opening with a connector gets the comma its pause needs", () => {
  assert.equal(
    said("Sin embargo funciona. Por lo tanto sirve", "es"),
    "Sin embargo, funciona. Por lo tanto, sirve.",
  );
  assert.equal(
    said("Sin embargo, ya está. Además lo vimos", "es"),
    "Sin embargo, ya está. Además, lo vimos.",
  );
  assert.equal(
    said("However it works. On the other hand it fails", "en"),
    "However, it works. On the other hand, it fails.",
  );
  assert.equal(said("Además.", "es"), "Además.", "alone it needs none");
  assert.equal(
    said("Dijo sin embargo algo", "es"),
    "Dijo sin embargo algo.",
    "only at the start of a sentence",
  );
});

test("another language gets its punctuation only", () => {
  assert.equal(
    said("Bonjour - le Dr. 25: ok", "fr"),
    "Bonjour, le Dr. 25, ok.",
  );
  assert.equal(said("Das ist (gut)", "de-DE"), "Das ist, gut.");
});

test("a text with nothing to read in it comes back as it was", () => {
  assert.equal(said("...", "es"), "...");
  assert.equal(said("  ", "en"), "  ");
  assert.equal(said("", "es"), "");
  assert.equal(said("¿?", "es"), "¿?");
});

test("a title at the end of a piece says the sentence goes on", () => {
  for (const piece of [
    "Vino el Sr.",
    "Ask Dr.",
    "with Mrs.",
    "Ver pág.",
    "see fig.",
    "like e.g.",
    "es decir p. ej.",
    "En EE. UU.",
    "(Prof.",
    "dijo «Dra.",
  ]) {
    assert.equal(m.endsInAbbreviation(piece), true, piece);
  }
  for (const piece of [
    "Dije que no.",
    "Terminó.",
    "Se lo dije al Señor.",
    "Doctor.",
    "El Sr",
    "Mister.",
  ]) {
    assert.equal(m.endsInAbbreviation(piece), false, piece);
  }
});

test("an abbreviation's full stop does not cut a sentence in two", () => {
  assert.deepEqual(
    speakable.sentences(
      "El Sr. García llegó. Se sentó. Habló con la Dra. Vera",
    ),
    ["El Sr. García llegó.", "Se sentó.", "Habló con la Dra. Vera"],
  );
  assert.deepEqual(speakable.sentences("Hola. Dijo que sí. Ver pág."), [
    "Hola.",
    "Dijo que sí.",
    "Ver pág.",
  ]);
  assert.deepEqual(
    speakable.sentences("Habló el Dr. Ruiz.\n\nLuego Mr. Smith"),
    ["Habló el Dr. Ruiz.", "Luego Mr. Smith"],
  );
});
