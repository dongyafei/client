import WotTask from '.'
import * as React from 'react'
import * as Sb from '../../stories/storybook'
import {action} from '../../stories/storybook'
import {WotStatusType} from '../../constants/types/rpc-gen'

const baseProps = {
  badged: true,
  key: '{voucher:alice,vouchee:bob}',
  onClickUser: action('onClickUser'),
  onDismiss: action('onDismiss'),
  vouchee: 'bob',
  voucher: 'alice',
}

const propsProposed = {
  ...baseProps,
  otherUser: 'alice',
  userForIcon: 'alice',
  wotStatus: WotStatusType.proposed,
}

const propsAccepted = {
  ...baseProps,
  otherUser: 'bob',
  userForIcon: 'bob',
  wotStatus: WotStatusType.accepted,
}

const propsRejected = {
  ...baseProps,
  otherUser: 'bob',
  userForIcon: 'bob',
  wotStatus: WotStatusType.rejected,
}

const load = () => {
  Sb.storiesOf('People/Wot Tasks', module)
    .add('proposed', () => <WotTask {...propsProposed} />)
    .add('accepted', () => <WotTask {...propsAccepted} />)
    .add('rejected', () => <WotTask {...propsRejected} />)
}

export default load
