// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { render } from 'preact'
import { act } from 'preact/test-utils'
import { ActionTrail, actionLabel } from './ActionTrail'

describe('actionLabel', () => {
  it('uses singular and plural', () => {
    expect(actionLabel(1)).toBe('1 action')
    expect(actionLabel(3)).toBe('3 actions')
  })
})

describe('ActionTrail', () => {
  it('is collapsed by default, shows the counter, and toggles on click', () => {
    const root = document.createElement('div')
    document.body.appendChild(root)
    act(() => {
      render(<ActionTrail count={3}><div class="detail">ls -la</div></ActionTrail>, root)
    })
    const btn = root.querySelector('button.action-trail-toggle') as HTMLButtonElement
    expect(btn.textContent).toContain('3 actions')
    expect(btn.getAttribute('aria-expanded')).toBe('false')
    expect(root.querySelector('.detail')).toBeNull()

    act(() => { btn.click() })
    expect(btn.getAttribute('aria-expanded')).toBe('true')
    expect(root.querySelector('.detail')?.textContent).toBe('ls -la')

    act(() => { btn.click() })
    expect(btn.getAttribute('aria-expanded')).toBe('false')
    expect(root.querySelector('.detail')).toBeNull()
  })
})
