// The UI language. Every text is written in both languages where it is
// used, t('Deutsch', 'English'), so there is no catalogue to keep in step.

export const lang = $state({
  // Until the app's setting arrives, follow the browser (= Windows) language.
  current: (navigator.language.toLowerCase().startsWith('de') ? 'de' : 'en') as 'de' | 'en',
})

export function t(de: string, en: string): string {
  return lang.current === 'en' ? en : de
}

export function locale(): string {
  return lang.current === 'en' ? 'en-GB' : 'de-DE'
}

/** A number in the current language's notation. */
export function num(n: number): string {
  return n.toLocaleString(locale())
}

/** "1 Bild" / "5 Bilder", "1 photo" / "5 photos". */
export function photos(n: number): string {
  return n === 1 ? t('1 Bild', '1 photo') : t(`${num(n)} Bilder`, `${num(n)} photos`)
}
